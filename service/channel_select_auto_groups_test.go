package service

import (
	"fmt"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupChannelSelectAutoGroupsTest(t *testing.T) *gorm.DB {
	t.Helper()

	originalDB := model.DB
	originalMemoryCacheEnabled := common.MemoryCacheEnabled
	originalRetryTimes := common.RetryTimes
	originalAutoGroups := setting.AutoGroups2JsonString()
	originalUsableGroups := setting.UserUsableGroups2JSONString()
	originalGroupRatios := ratio_setting.GroupRatio2JSONString()
	originalMaxTokenAutoGroups := setting.GetMaxTokenAutoGroups()

	originalSQLitePath := common.SQLitePath
	originalMaster := common.IsMasterNode
	originalMainType, originalLogType := common.MainDatabaseType(), common.LogDatabaseType()
	common.SQLitePath = fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	common.IsMasterNode = false
	t.Setenv("SQL_DSN", os.Getenv("NEW_API_ROUTING_TEST_DSN"))
	t.Setenv("LOG_SQL_DSN", "")
	require.NoError(t, model.InitDB())
	db := model.DB
	require.NoError(t, db.AutoMigrate(&model.Channel{}, &model.Ability{}))
	// Keep external test databases reusable without retaining fixture data.
	tx := db.Begin()
	require.NoError(t, tx.Error)
	model.DB = tx
	t.Cleanup(func() {
		common.SQLitePath = originalSQLitePath
		common.IsMasterNode = originalMaster
		common.SetDatabaseTypes(originalMainType, originalLogType)
	})
	common.MemoryCacheEnabled = true
	common.RetryTimes = 0

	require.NoError(t, setting.UpdateAutoGroupsByJsonString(`[]`))
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"default":"Default","vip":"VIP"}`))
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1,"vip":2}`))
	require.NoError(t, setting.UpdateMaxTokenAutoGroups("2"))

	t.Cleanup(func() {
		require.NoError(t, tx.Rollback().Error)
		model.DB = originalDB
		common.MemoryCacheEnabled = originalMemoryCacheEnabled
		common.RetryTimes = originalRetryTimes
		require.NoError(t, setting.UpdateAutoGroupsByJsonString(originalAutoGroups))
		require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(originalUsableGroups))
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(originalGroupRatios))
		require.NoError(t, setting.UpdateMaxTokenAutoGroups(fmt.Sprintf("%d", originalMaxTokenAutoGroups)))

		if originalMemoryCacheEnabled && originalDB != nil &&
			originalDB.Migrator().HasTable(&model.Channel{}) && originalDB.Migrator().HasTable(&model.Ability{}) {
			model.InitChannelCache()
		}
		sqlDB, err := db.DB()
		if err == nil {
			require.NoError(t, sqlDB.Close())
		}
	})

	return tx
}

func createChannelSelectAutoGroupsChannel(t *testing.T, db *gorm.DB, id int, group, modelName string) {
	t.Helper()
	priority := int64(0)
	weight := uint(100)
	require.NoError(t, db.Create(&model.Channel{
		Id:       id,
		Type:     constant.ChannelTypeOpenAI,
		Key:      fmt.Sprintf("key-%d", id),
		Status:   common.ChannelStatusEnabled,
		Name:     fmt.Sprintf("channel-%d", id),
		Weight:   &weight,
		Models:   modelName,
		Group:    group,
		Priority: &priority,
	}).Error)
	require.NoError(t, db.Create(&model.Ability{
		Group:     group,
		Model:     modelName,
		ChannelId: id,
		Enabled:   true,
		Priority:  &priority,
		Weight:    weight,
	}).Error)
}

func TestCacheGetRandomSatisfiedChannelUsesTokenAutoGroupsWhenGlobalAutoIsEmpty(t *testing.T) {
	db := setupChannelSelectAutoGroupsTest(t)
	const modelName = "auto-groups-runtime-model"
	createChannelSelectAutoGroupsChannel(t, db, 2101, "vip", modelName)
	createChannelSelectAutoGroupsChannel(t, db, 2102, "default", modelName)
	model.InitChannelCache()

	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	common.SetContextKey(ctx, constant.ContextKeyUserGroup, "default")
	common.SetContextKey(ctx, constant.ContextKeyTokenAutoGroups, []string{"vip", "default"})
	common.SetContextKey(ctx, constant.ContextKeyTokenCrossGroupRetry, true)

	retry := 0
	param := &RetryParam{
		Ctx:         ctx,
		TokenGroup:  "auto",
		ModelName:   modelName,
		RequestPath: "/v1/chat/completions",
		Retry:       &retry,
	}

	first, selectedGroup, err := CacheGetRandomSatisfiedChannel(param)
	require.NoError(t, err)
	require.NotNil(t, first)
	assert.Equal(t, 2101, first.Id)
	assert.Equal(t, "vip", selectedGroup)
	assert.Equal(t, "vip", common.GetContextKeyString(ctx, constant.ContextKeyAutoGroup))
	assert.Empty(t, setting.GetAutoGroups(), "the selection must not depend on the global Auto list")

	param.IncreaseRetry()
	second, selectedGroup, err := CacheGetRandomSatisfiedChannel(param)
	require.NoError(t, err)
	require.NotNil(t, second)
	assert.Equal(t, 2102, second.Id)
	assert.Equal(t, "default", selectedGroup)
	assert.Equal(t, "default", common.GetContextKeyString(ctx, constant.ContextKeyAutoGroup))
}

// The policy must never expand model/group access, revisit failed channels, or
// let a sticky fallback bypass a recovered higher tier.
func TestRoutingPolicyOrderIsolationAndRecovery(t *testing.T) {
	db := setupChannelSelectAutoGroupsTest(t)
	oldPolicy := operation_setting.RoutingPolicyJSON()
	t.Cleanup(func() { require.NoError(t, operation_setting.UpdateRoutingPolicy(oldPolicy)); routingCooldowns.Clear() })
	require.NoError(t, operation_setting.UpdateRoutingPolicy(`{"enabled":true,"group_tag_order":{"team":["free","plan"],"owner":["plan","free","metered"]},"max_attempts_per_tag":2,"rate_limit_cooldown_seconds":60,"quota_cooldown_seconds":3600,"quota_error_keywords":["weekly"],"request_timeout_seconds":300}`))
	common.RetryTimes = 5
	for _, f := range []struct {
		id                int
		tag, group, model string
	}{{3101, "free", "team", "m"}, {3102, "plan", "team", "m"}, {3103, "metered", "team", "m"}, {3104, "free", "owner", "m"}, {3105, "plan", "owner", "m"}, {3106, "metered", "owner", "m"}, {3107, "free", "owner", "other"}} {
		createChannelSelectAutoGroupsChannel(t, db, f.id, f.group, f.model)
		require.NoError(t, db.Model(&model.Channel{}).Where("id = ?", f.id).Update("tag", f.tag).Error)
	}
	model.InitChannelCache()
	for _, cache := range []bool{true, false} {
		t.Run(fmt.Sprintf("cache=%v", cache), func(t *testing.T) {
			common.MemoryCacheEnabled = cache
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			ctx.Request = httptest.NewRequest("POST", "/v1/messages", nil)
			common.SetContextKey(ctx, constant.ContextKeyUserGroup, "team")
			p := &RetryParam{Ctx: ctx, TokenGroup: "team", ModelName: "m"}
			first, _, err := CacheGetRandomSatisfiedChannel(p)
			require.NoError(t, err)
			require.NotNil(t, first)
			assert.Equal(t, 3101, first.Id)
			ctx.Set("use_channel", []string{"3101"})
			p.SetRetry(1)
			next, _, err := CacheGetRandomSatisfiedChannel(p)
			require.NoError(t, err)
			require.NotNil(t, next)
			assert.Equal(t, 3102, next.Id)
			ctx.Set("use_channel", []string{"3101", "3102"})
			p.SetRetry(2)
			none, _, err := CacheGetRandomSatisfiedChannel(p)
			require.NoError(t, err)
			assert.Nil(t, none, "unlisted metered tag must never be selected")
			ctx.Set("use_channel", []string{})
			p.TokenGroup = "owner"
			p.SetRetry(0)
			first, _, err = CacheGetRandomSatisfiedChannel(p)
			require.NoError(t, err)
			require.NotNil(t, first)
			assert.Equal(t, 3105, first.Id)
			fallback, err := model.CacheGetChannel(3106)
			require.NoError(t, err)
			assert.False(t, RoutingAffinityAllowed(ctx, fallback, "m", "owner"))
			RecordRoutingFailure(ctx, 3105, "m", 429, "weekly quota exceeded")
			first, _, err = CacheGetRandomSatisfiedChannel(p)
			require.NoError(t, err)
			require.NotNil(t, first)
			assert.Equal(t, 3104, first.Id)
			p.ModelName = "forbidden"
			none, _, err = CacheGetRandomSatisfiedChannel(p)
			require.NoError(t, err)
			assert.Nil(t, none)
			routingCooldowns.Clear()
		})
	}
}

func TestRoutingPolicyReservesFallbackAndExpiresCooldown(t *testing.T) {
	db := setupChannelSelectAutoGroupsTest(t)
	old := operation_setting.RoutingPolicyJSON()
	t.Cleanup(func() { require.NoError(t, operation_setting.UpdateRoutingPolicy(old)); routingCooldowns.Clear() })
	require.NoError(t, operation_setting.UpdateRoutingPolicy(`{"enabled":true,"group_tag_order":{"team":["free","plan","backup"]},"max_attempts_per_tag":3,"rate_limit_cooldown_seconds":60,"quota_cooldown_seconds":3600,"quota_error_keywords":["weekly"],"request_timeout_seconds":300}`))
	common.RetryTimes = 3
	for _, f := range []struct {
		id  int
		tag string
	}{{3201, "free"}, {3202, "free"}, {3203, "free"}, {3204, "plan"}, {3205, "backup"}} {
		createChannelSelectAutoGroupsChannel(t, db, f.id, "team", "m")
		require.NoError(t, db.Model(&model.Channel{}).Where("id = ?", f.id).Update("tag", f.tag).Error)
	}
	model.InitChannelCache()
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest("POST", "/v1/messages", nil)
	p := &RetryParam{Ctx: ctx, TokenGroup: "team", ModelName: "m"}
	ctx.Set("use_channel", []string{"3201", "3202"})
	p.SetRetry(2)
	next, _, err := CacheGetRandomSatisfiedChannel(p)
	require.NoError(t, err)
	require.NotNil(t, next)
	assert.Equal(t, 3204, next.Id, "reserve last attempt for backup")
	ctx.Set("use_channel", []string{"3201", "3202", "3204"})
	p.SetRetry(3)
	next, _, err = CacheGetRandomSatisfiedChannel(p)
	require.NoError(t, err)
	require.NotNil(t, next)
	assert.Equal(t, 3205, next.Id)
	ctx.Set("use_channel", []string{"3201", "3202", "3203"})
	p.SetRetry(3)
	next, _, err = CacheGetRandomSatisfiedChannel(p)
	require.NoError(t, err)
	require.NotNil(t, next)
	assert.Equal(t, 3204, next.Id, "attempt cap skips free tier")
	RecordRoutingFailure(ctx, 3204, "m", 429, "weekly quota")
	key := routingCooldownKey{3204, "m"}
	expiry, _ := routingCooldowns.Load(key)
	RecordRoutingFailure(ctx, 3204, "m", 429, "rate limit")
	unchanged, _ := routingCooldowns.Load(key)
	assert.Equal(t, expiry, unchanged, "short cooldown must not shorten quota cooldown")
	ctx.Set("use_channel", []string{"3201", "3204"})
	p.SetRetry(2)
	next, _, err = CacheGetRandomSatisfiedChannel(p)
	require.NoError(t, err)
	require.NotNil(t, next)
	assert.Equal(t, 3205, next.Id, "a request must not return to an earlier tier")
	ctx.Set("use_channel", []string{"3201", "3202", "3203"})
	p.SetRetry(3)
	routingCooldowns.Store(key, time.Now().Add(-time.Second))
	next, _, err = CacheGetRandomSatisfiedChannel(p)
	require.NoError(t, err)
	require.NotNil(t, next)
	assert.Equal(t, 3204, next.Id, "expired cooldown restores eligible channel")
}

func TestRoutingPolicyRejectsInvalidConfiguration(t *testing.T) {
	original := operation_setting.RoutingPolicyJSON()
	t.Cleanup(func() { require.NoError(t, operation_setting.UpdateRoutingPolicy(original)) })
	for _, mutation := range []func(map[string]any){
		func(p map[string]any) { p["max_attempts_per_tag"] = -1 },
		func(p map[string]any) { p["quota_cooldown_seconds"] = -1 },
		func(p map[string]any) { p["max_total_attempts"] = 1025 },
		func(p map[string]any) { p["request_timeout_seconds"] = 1801 },
		func(p map[string]any) { p["group_tag_order"] = map[string][]string{"team": {"free", "free"}} },
	} {
		var policy map[string]any
		require.NoError(t, common.UnmarshalJsonStr(original, &policy))
		mutation(policy)
		raw, err := common.Marshal(policy)
		require.NoError(t, err)
		require.Error(t, operation_setting.UpdateRoutingPolicy(string(raw)))
		assert.Equal(t, original, operation_setting.RoutingPolicyJSON(), "invalid update must not change active policy")
	}
}

func TestRoutingPolicyExhaustsProviderAccountsBeforeFallback(t *testing.T) {
	db := setupChannelSelectAutoGroupsTest(t)
	original := operation_setting.RoutingPolicyJSON()
	t.Cleanup(func() { require.NoError(t, operation_setting.UpdateRoutingPolicy(original)); routingCooldowns.Clear() })
	const policy = `{"enabled":true,"group_tag_order":{"team":["free","plan"]},"max_attempts_per_tag":0,"max_total_attempts":32,"rate_limit_cooldown_seconds":60,"quota_cooldown_seconds":3600,"quota_error_keywords":[],"request_timeout_seconds":300}`
	require.NoError(t, operation_setting.UpdateRoutingPolicy(policy))
	common.RetryTimes = 7
	for id := 4101; id <= 4112; id++ {
		createChannelSelectAutoGroupsChannel(t, db, id, "team", "m")
		require.NoError(t, db.Model(&model.Channel{}).Where("id = ?", id).Update("tag", "free").Error)
	}
	createChannelSelectAutoGroupsChannel(t, db, 4201, "team", "m")
	require.NoError(t, db.Model(&model.Channel{}).Where("id = ?", 4201).Update("tag", "plan").Error)
	for _, cache := range []bool{true, false} {
		for _, skipUnavailable := range []bool{false, true} {
			t.Run(fmt.Sprintf("cache=%v/unavailable=%v", cache, skipUnavailable), func(t *testing.T) {
				routingCooldowns.Clear()
				common.MemoryCacheEnabled = cache
				require.NoError(t, db.Model(&model.Channel{}).Where("id = ?", 4101).Update("status", common.ChannelStatusEnabled).Error)
				require.NoError(t, db.Model(&model.Ability{}).Where("channel_id = ?", 4101).Update("enabled", true).Error)
				expected := 12
				if skipUnavailable {
					require.NoError(t, db.Model(&model.Channel{}).Where("id = ?", 4101).Update("status", common.ChannelStatusAutoDisabled).Error)
					require.NoError(t, db.Model(&model.Ability{}).Where("channel_id = ?", 4101).Update("enabled", false).Error)
					routingCooldowns.Store(routingCooldownKey{4102, "m"}, time.Now().Add(time.Hour))
					expected = 10
				}
				model.InitChannelCache()
				ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
				ctx.Request = httptest.NewRequest("POST", "/v1/messages", nil)
				common.SetContextKey(ctx, constant.ContextKeyUsingGroup, "team")
				p := &RetryParam{Ctx: ctx, TokenGroup: "team", ModelName: "m"}
				used := []string{}
				for attempt := 0; attempt < expected; attempt++ {
					p.SetRetry(attempt)
					ch, _, err := CacheGetRandomSatisfiedChannel(p)
					require.NoError(t, err)
					require.NotNil(t, ch)
					assert.Equal(t, "free", *ch.Tag, "provider must not be skipped at the old three/eight-attempt limits")
					id := fmt.Sprint(ch.Id)
					assert.NotContains(t, used, id)
					if skipUnavailable {
						assert.NotContains(t, []int{4101, 4102}, ch.Id)
					}
					used = append(used, id)
					ctx.Set("use_channel", used)
				}
				p.SetRetry(expected)
				ch, _, err := CacheGetRandomSatisfiedChannel(p)
				require.NoError(t, err)
				require.NotNil(t, ch)
				assert.Equal(t, 4201, ch.Id)
			})
		}
	}
}

func TestRoutingPolicyTotalBudgetDoesNotCauseEarlyFallback(t *testing.T) {
	db := setupChannelSelectAutoGroupsTest(t)
	original := operation_setting.RoutingPolicyJSON()
	t.Cleanup(func() { require.NoError(t, operation_setting.UpdateRoutingPolicy(original)); routingCooldowns.Clear() })
	require.NoError(t, operation_setting.UpdateRoutingPolicy(`{"enabled":true,"group_tag_order":{"team":["free","plan"]},"max_attempts_per_tag":0,"max_total_attempts":2,"rate_limit_cooldown_seconds":0,"quota_cooldown_seconds":0,"quota_error_keywords":[],"request_timeout_seconds":300}`))
	common.RetryTimes = 7
	for _, fixture := range []struct {
		id  int
		tag string
	}{{4301, "free"}, {4302, "free"}, {4303, "free"}, {4401, "plan"}} {
		createChannelSelectAutoGroupsChannel(t, db, fixture.id, "team", "m")
		require.NoError(t, db.Model(&model.Channel{}).Where("id = ?", fixture.id).Update("tag", fixture.tag).Error)
	}
	model.InitChannelCache()
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest("POST", "/v1/messages", nil)
	common.SetContextKey(ctx, constant.ContextKeyUsingGroup, "team")
	ctx.Set("use_channel", []string{"4301"})
	p := &RetryParam{Ctx: ctx, TokenGroup: "team", ModelName: "m", Retry: common.GetPointer(1)}
	ch, _, err := CacheGetRandomSatisfiedChannel(p)
	require.NoError(t, err)
	require.NotNil(t, ch)
	assert.Equal(t, "free", *ch.Tag)
	ctx.Set("use_channel", []string{"4301", fmt.Sprint(ch.Id)})
	p.SetRetry(0) // Auto-group retry counters can reset; total attempts cannot.
	ch, _, err = CacheGetRandomSatisfiedChannel(p)
	require.NoError(t, err)
	assert.Nil(t, ch, "budget exhaustion must stop without trying plan")
}

func TestRoutingAttemptLimitAppliesOnlyToConfiguredRequests(t *testing.T) {
	setupChannelSelectAutoGroupsTest(t)
	original := operation_setting.RoutingPolicyJSON()
	t.Cleanup(func() { require.NoError(t, operation_setting.UpdateRoutingPolicy(original)) })
	require.NoError(t, operation_setting.UpdateRoutingPolicy(`{"enabled":true,"group_tag_order":{"vip":["free","plan"]},"max_attempts_per_tag":0,"max_total_attempts":32,"rate_limit_cooldown_seconds":0,"quota_cooldown_seconds":0,"quota_error_keywords":[],"request_timeout_seconds":300}`))
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	common.SetContextKey(ctx, constant.ContextKeyUserGroup, "default")
	common.SetContextKey(ctx, constant.ContextKeyUsingGroup, "default")
	assert.Zero(t, RoutingAttemptLimit(ctx), "unconfigured groups keep native retries")
	common.SetContextKey(ctx, constant.ContextKeyUsingGroup, "vip")
	assert.Equal(t, 32, RoutingAttemptLimit(ctx))
	common.SetContextKey(ctx, constant.ContextKeyUsingGroup, "auto")
	common.SetContextKey(ctx, constant.ContextKeyTokenAutoGroups, []string{"vip"})
	assert.Equal(t, 32, RoutingAttemptLimit(ctx), "auto routing includes the configured candidate group")
	common.SetContextKey(ctx, constant.ContextKeyTokenAutoGroups, []string{"default"})
	assert.Zero(t, RoutingAttemptLimit(ctx))
	assert.Zero(t, RoutingAttemptLimit(nil))
}

func TestRoutingPolicyExhaustsAutoGroupBeforeAdvancing(t *testing.T) {
	db := setupChannelSelectAutoGroupsTest(t)
	original := operation_setting.RoutingPolicyJSON()
	t.Cleanup(func() { require.NoError(t, operation_setting.UpdateRoutingPolicy(original)); routingCooldowns.Clear() })
	require.NoError(t, operation_setting.UpdateRoutingPolicy(`{"enabled":true,"group_tag_order":{"vip":["free"],"default":["free"]},"max_attempts_per_tag":0,"max_total_attempts":32,"rate_limit_cooldown_seconds":0,"quota_cooldown_seconds":0,"quota_error_keywords":[],"request_timeout_seconds":300}`))
	common.RetryTimes = 7
	for id := 4501; id <= 4512; id++ {
		createChannelSelectAutoGroupsChannel(t, db, id, "vip", "m")
		require.NoError(t, db.Model(&model.Channel{}).Where("id = ?", id).Update("tag", "free").Error)
	}
	createChannelSelectAutoGroupsChannel(t, db, 4601, "default", "m")
	require.NoError(t, db.Model(&model.Channel{}).Where("id = ?", 4601).Update("tag", "free").Error)
	model.InitChannelCache()
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest("POST", "/v1/messages", nil)
	common.SetContextKey(ctx, constant.ContextKeyUserGroup, "default")
	common.SetContextKey(ctx, constant.ContextKeyUsingGroup, "auto")
	common.SetContextKey(ctx, constant.ContextKeyTokenAutoGroups, []string{"vip", "default"})
	common.SetContextKey(ctx, constant.ContextKeyTokenCrossGroupRetry, true)
	p := &RetryParam{Ctx: ctx, TokenGroup: "auto", ModelName: "m"}
	used := []string{}
	for i := 0; i < 12; i++ {
		ch, group, err := CacheGetRandomSatisfiedChannel(p)
		require.NoError(t, err)
		require.NotNil(t, ch)
		require.Equal(t, "vip", group, "legacy retry limit must not advance an unexhausted policy group")
		id := fmt.Sprint(ch.Id)
		assert.NotContains(t, used, id)
		used = append(used, id)
		ctx.Set("use_channel", used)
		p.IncreaseRetry()
	}
	ch, group, err := CacheGetRandomSatisfiedChannel(p)
	require.NoError(t, err)
	require.NotNil(t, ch)
	assert.Equal(t, "default", group)
	assert.Equal(t, 4601, ch.Id)
}

func TestRoutingPolicyKeepsLegacyFiniteAutoGroupRetry(t *testing.T) {
	db := setupChannelSelectAutoGroupsTest(t)
	original := operation_setting.RoutingPolicyJSON()
	t.Cleanup(func() { require.NoError(t, operation_setting.UpdateRoutingPolicy(original)); routingCooldowns.Clear() })
	require.NoError(t, operation_setting.UpdateRoutingPolicy(`{"enabled":true,"group_tag_order":{"vip":["free"],"default":["free"]},"max_attempts_per_tag":3,"rate_limit_cooldown_seconds":0,"quota_cooldown_seconds":0,"quota_error_keywords":[],"request_timeout_seconds":300}`))
	common.RetryTimes = 1
	for _, fixture := range []struct {
		id    int
		group string
	}{{4701, "vip"}, {4702, "vip"}, {4703, "vip"}, {4801, "default"}} {
		createChannelSelectAutoGroupsChannel(t, db, fixture.id, fixture.group, "m")
		require.NoError(t, db.Model(&model.Channel{}).Where("id = ?", fixture.id).Update("tag", "free").Error)
	}
	model.InitChannelCache()
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest("POST", "/v1/messages", nil)
	common.SetContextKey(ctx, constant.ContextKeyUserGroup, "default")
	common.SetContextKey(ctx, constant.ContextKeyUsingGroup, "auto")
	common.SetContextKey(ctx, constant.ContextKeyTokenAutoGroups, []string{"vip", "default"})
	common.SetContextKey(ctx, constant.ContextKeyTokenCrossGroupRetry, true)
	p := &RetryParam{Ctx: ctx, TokenGroup: "auto", ModelName: "m"}
	used := []string{}
	groups := []string{}
	for p.GetRetry() <= common.RetryTimes && len(groups) < 3 {
		ch, group, err := CacheGetRandomSatisfiedChannel(p)
		require.NoError(t, err)
		require.NotNil(t, ch)
		groups = append(groups, group)
		used = append(used, fmt.Sprint(ch.Id))
		ctx.Set("use_channel", used)
		p.IncreaseRetry()
	}
	assert.Equal(t, []string{"vip", "vip", "default"}, groups)
	assert.Zero(t, RoutingAttemptLimit(ctx), "old saved policies retain native cross-group retry behavior")
}
