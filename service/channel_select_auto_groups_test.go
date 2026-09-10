package service

import (
	"context"
	"fmt"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	kitdto "github.com/QuantumNous/new-api/relaykit/dto"
	kittypes "github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
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

func useRoutingMiniRedis(t *testing.T) *miniredis.Miniredis {
	t.Helper()
	previousEnabled := common.RedisEnabled
	previousClient := common.RDB
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	require.NoError(t, client.Ping(context.Background()).Err())
	common.RedisEnabled = true
	common.RDB = client
	t.Cleanup(func() {
		_ = client.Close()
		common.RedisEnabled = previousEnabled
		common.RDB = previousClient
		routingCooldowns.Clear()
		routingLoadBuckets.Clear()
		routingRateLimitStrikes.Clear()
	})
	return server
}

func setRoutingTestAccount(t *testing.T, db *gorm.DB, id int, tag, account string) {
	t.Helper()
	setting := fmt.Sprintf(`{"protocol_routing":{"enabled":true,"account_resource":%q,"quota_scope":"model","defaults":{"entry_formats":["openai"],"endpoints":[{"format":"openai","path":"/v1/chat/completions","verified":true}]}}}`, account)
	require.NoError(t, db.Model(&model.Channel{}).Where("id = ?", id).Updates(map[string]any{
		"tag":     tag,
		"setting": setting,
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
	key := routingCooldownKey{"channel:3204", "m"}
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
	routingCooldowns.Delete(routingCooldownKey{"channel:3204", ""})
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
		func(p map[string]any) {
			p["tag_capacity"] = map[string]any{"free": map[string]any{"window_seconds": 9, "max_requests": 1}}
		},
		func(p map[string]any) {
			p["tag_capacity"] = map[string]any{"free": map[string]any{"window_seconds": 60}}
		},
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

func TestRoutingPolicyBalancesAccountLoadBeforePaidFallback(t *testing.T) {
	db := setupChannelSelectAutoGroupsTest(t)
	useRoutingMiniRedis(t)
	original := operation_setting.RoutingPolicyJSON()
	t.Cleanup(func() { require.NoError(t, operation_setting.UpdateRoutingPolicy(original)) })
	require.NoError(t, operation_setting.UpdateRoutingPolicy(`{"enabled":true,"group_tag_order":{"team":["free","plan"]},"tag_capacity":{"free":{"window_seconds":60,"max_requests":10,"max_input_tokens":1000}},"max_attempts_per_tag":0,"max_total_attempts":32,"rate_limit_cooldown_seconds":60,"rate_limit_max_cooldown_seconds":600,"quota_cooldown_seconds":3600,"quota_error_keywords":[],"request_timeout_seconds":300}`))

	for _, fixture := range []struct {
		id      int
		tag     string
		account string
	}{{6101, "free", "free-a"}, {6102, "free", "free-b"}, {6103, "free", "free-c"}, {6201, "plan", "plan-a"}} {
		createChannelSelectAutoGroupsChannel(t, db, fixture.id, "team", "m")
		setRoutingTestAccount(t, db, fixture.id, fixture.tag, fixture.account)
	}
	createChannelSelectAutoGroupsChannel(t, db, 6104, "team", "other-model")
	setRoutingTestAccount(t, db, 6104, "free", "free-a")
	model.InitChannelCache()
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest("POST", "/v1/messages", nil)
	common.SetContextKey(ctx, constant.ContextKeyEstimatedTokens, 400)
	param := &RetryParam{Ctx: ctx, TokenGroup: "team", ModelName: "m"}

	RecordRoutingAttempt(ctx, 6101, "m", 500)
	RecordRoutingAttempt(ctx, 6102, "m", 100)
	RecordRoutingAttempt(ctx, 6103, "m", 900)
	selected, _, err := CacheGetRandomSatisfiedChannel(param)
	require.NoError(t, err)
	require.NotNil(t, selected)
	assert.Equal(t, 6102, selected.Id, "the lowest projected load in the highest tag wins")
	RecordRoutingAttempt(ctx, 6104, "other-model", 200)
	assert.False(t, RoutingAffinityAllowed(ctx, mustRoutingChannel(t, 6101), "m", "team"), "an affinity account that would exceed capacity must migrate")

	RecordRoutingAttempt(ctx, 6102, "m", 600)
	selected, _, err = CacheGetRandomSatisfiedChannel(param)
	require.NoError(t, err)
	require.NotNil(t, selected)
	assert.Equal(t, 6201, selected.Id, "paid fallback begins only after every free account is locally saturated")
}

func mustRoutingChannel(t *testing.T, id int) *model.Channel {
	t.Helper()
	channel, err := model.CacheGetChannel(id)
	require.NoError(t, err)
	return channel
}

func TestRoutingPolicyPersistsCooldownAndBacksOffRepeated429(t *testing.T) {
	db := setupChannelSelectAutoGroupsTest(t)
	redisServer := useRoutingMiniRedis(t)
	original := operation_setting.RoutingPolicyJSON()
	t.Cleanup(func() { require.NoError(t, operation_setting.UpdateRoutingPolicy(original)) })
	require.NoError(t, operation_setting.UpdateRoutingPolicy(`{"enabled":true,"group_tag_order":{"team":["free","plan"]},"max_attempts_per_tag":0,"max_total_attempts":32,"rate_limit_cooldown_seconds":60,"rate_limit_max_cooldown_seconds":600,"quota_cooldown_seconds":3600,"quota_error_keywords":[],"request_timeout_seconds":300}`))
	for _, fixture := range []struct {
		id      int
		tag     string
		account string
	}{{6301, "free", "free-a"}, {6401, "plan", "plan-a"}} {
		createChannelSelectAutoGroupsChannel(t, db, fixture.id, "team", "m")
		setRoutingTestAccount(t, db, fixture.id, fixture.tag, fixture.account)
	}
	model.InitChannelCache()
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest("POST", "/v1/messages", nil)
	param := &RetryParam{Ctx: ctx, TokenGroup: "team", ModelName: "m"}

	RecordRoutingFailure(ctx, 6301, "m", 429, "inference exceeds tpm/rpm limit")
	routingCooldowns.Clear() // simulate a new process; Redis remains authoritative
	selected, _, err := CacheGetRandomSatisfiedChannel(param)
	require.NoError(t, err)
	require.NotNil(t, selected)
	assert.Equal(t, 6401, selected.Id)

	redisServer.FastForward(61 * time.Second)
	RecordRoutingFailure(ctx, 6301, "m", 429, "inference exceeds tpm/rpm limit")
	var cooldownTTL time.Duration
	for _, key := range redisServer.Keys() {
		if strings.Contains(key, ":cooldown:") {
			cooldownTTL = redisServer.TTL(key)
		}
	}
	assert.GreaterOrEqual(t, cooldownTTL, 119*time.Second)
	assert.LessOrEqual(t, cooldownTTL, 120*time.Second)
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
					routingCooldowns.Store(routingCooldownKey{"channel:4102", "m"}, time.Now().Add(time.Hour))
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

func TestRoutingPolicySharedAccountCooldownAndExclusion(t *testing.T) {
	db := setupChannelSelectAutoGroupsTest(t)
	original := operation_setting.RoutingPolicyJSON()
	t.Cleanup(func() { require.NoError(t, operation_setting.UpdateRoutingPolicy(original)); routingCooldowns.Clear() })
	require.NoError(t, operation_setting.UpdateRoutingPolicy(`{"enabled":true,"group_tag_order":{"team":["free","plan"]},"max_attempts_per_tag":0,"max_total_attempts":32,"rate_limit_cooldown_seconds":60,"quota_cooldown_seconds":3600,"quota_error_keywords":["weekly"],"request_timeout_seconds":300}`))
	for _, id := range []int{5101, 5102, 5103, 5201} {
		createChannelSelectAutoGroupsChannel(t, db, id, "team", "m")
		tag := "free"
		if id == 5201 {
			tag = "plan"
		}
		require.NoError(t, db.Model(&model.Channel{}).Where("id = ?", id).Update("tag", tag).Error)
	}
	require.NoError(t, db.Model(&model.Channel{}).Where("id = ?", 5101).Update("model_mapping", `{"m":"middle","middle":"upstream","public-alias":"middle"}`).Error)
	require.NoError(t, db.Model(&model.Channel{}).Where("id = ?", 5102).Update("model_mapping", `{"m":"upstream"}`).Error)
	for _, scope := range []string{"model", "account"} {
		for _, cache := range []bool{false, true} {
			t.Run(fmt.Sprintf("scope=%s/cache=%v", scope, cache), func(t *testing.T) {
				routingCooldowns.Clear()
				common.MemoryCacheEnabled = cache
				raw := fmt.Sprintf(`{"protocol_routing":{"enabled":true,"account_resource":"supplier/account-a","quota_scope":%q,"defaults":{"entry_formats":["openai"],"endpoints":[{"format":"openai","path":"/v1/chat/completions","verified":true}]}}}`, scope)
				require.NoError(t, db.Model(&model.Channel{}).Where("id IN ?", []int{5101, 5102}).Update("setting", raw).Error)
				model.InitChannelCache()
				ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
				ctx.Request = httptest.NewRequest("POST", "/v1/messages", nil)
				param := &RetryParam{Ctx: ctx, TokenGroup: "team", ModelName: "m"}
				failedModel := "public-alias@effort:high"
				if scope == "account" {
					failedModel = "another-model"
				}
				if scope == "model" {
					RecordRoutingFailure(ctx, 5101, "unrelated-model", 429, "weekly quota")
					alias, err := model.CacheGetChannel(5102)
					require.NoError(t, err)
					assert.True(t, RoutingAffinityAllowed(ctx, alias, "m", "team"), "model scope leaves unrelated models healthy")
					routingCooldowns.Clear()
				}
				RecordRoutingFailure(ctx, 5101, failedModel, 429, "weekly quota")
				if scope == "model" {
					// A differently scoped alias still honors this model's recorded failure.
					aliasRaw := strings.Replace(raw, `"quota_scope":"model"`, `"quota_scope":"account"`, 1)
					require.NoError(t, db.Model(&model.Channel{}).Where("id = ?", 5102).Update("setting", aliasRaw).Error)
					model.InitChannelCache()
				}
				selected, _, err := CacheGetRandomSatisfiedChannel(param)
				require.NoError(t, err)
				require.NotNil(t, selected)
				assert.Equal(t, 5103, selected.Id, "cooldown covers account aliases but leaves a separate account eligible")
				alias, err := model.CacheGetChannel(5102)
				require.NoError(t, err)
				assert.False(t, RoutingAffinityAllowed(ctx, alias, "m", "team"))
				ctx.Set("use_channel", []string{"5103"})
				selected, _, err = CacheGetRandomSatisfiedChannel(param)
				require.NoError(t, err)
				require.NotNil(t, selected)
				assert.Equal(t, 5201, selected.Id, "supplier advances only once every distinct account is unavailable")
				routingCooldowns.Clear()
				ctx.Set("use_channel", []string{"5101"})
				selected, _, err = CacheGetRandomSatisfiedChannel(param)
				require.NoError(t, err)
				require.NotNil(t, selected)
				assert.Equal(t, 5103, selected.Id, "another endpoint alias is not another account attempt")
				assert.False(t, RoutingAffinityAllowed(ctx, alias, "m", "team"), "affinity cannot revive an attempted account alias")
			})
		}
	}
	// Invalid mappings are not eligible routing candidates even with account scope.
	routingCooldowns.Clear()
	require.NoError(t, db.Model(&model.Channel{}).Where("id IN ?", []int{5101, 5102}).Update("model_mapping", `{"m":"private-cycle","private-cycle":"m"}`).Error)
	model.InitChannelCache()
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	selected, _, err := CacheGetRandomSatisfiedChannel(&RetryParam{Ctx: ctx, TokenGroup: "team", ModelName: "m"})
	require.NoError(t, err)
	require.NotNil(t, selected)
	assert.Equal(t, 5103, selected.Id)

}

func TestProtocolRoutingSettingsDatabasePersistence(t *testing.T) {
	db := setupChannelSelectAutoGroupsTest(t)
	versionQuery := "SELECT version()"
	if db.Dialector.Name() == "sqlite" {
		versionQuery = "SELECT sqlite_version()"
	}
	var version string
	require.NoError(t, db.Raw(versionQuery).Scan(&version).Error)
	t.Logf("protocol settings persistence database: %s %s", db.Dialector.Name(), version)

	settings := kitdto.ChannelSettings{
		Proxy: "http://127.0.0.1:7890", SystemPrompt: "Preserve this existing administrator prompt.",
		ProtocolRouting: &kitdto.ProtocolRoutingSettings{
			Enabled: true, AccountResource: "supplier/account-a", QuotaScope: "model",
			Defaults: kitdto.ProtocolModelPolicy{
				EntryFormats: []kittypes.RelayFormat{kittypes.RelayFormatOpenAI}, LossPolicy: "safe",
				Endpoints: []kitdto.ProtocolEndpoint{{Format: kittypes.RelayFormatOpenAI, Path: "/v1/chat/completions", Features: []string{"stream", "tools", "images"}, Verified: true, VerifiedAt: "2026-09-08T12:34:56+08:00"}},
			},
			Models: map[string]kitdto.ProtocolModelPolicy{"provider/model-x": {
				EntryFormats: []kittypes.RelayFormat{kittypes.RelayFormatClaude, kittypes.RelayFormatOpenAIResponses}, LossPolicy: "strict",
				Endpoints: []kitdto.ProtocolEndpoint{{Format: kittypes.RelayFormatOpenAIResponses, Path: "/v1/responses", Features: []string{"stream", "tools"}, Verified: true, VerifiedAt: "2026-09-08T04:34:56Z"}},
			}},
		},
	}
	channel := model.Channel{Type: constant.ChannelTypeOpenAI, Key: "persistence-fixture-key", Name: "protocol-settings-persistence", Models: "public-model", Group: "team", Status: common.ChannelStatusEnabled}
	channel.SetSetting(settings)
	require.NoError(t, channel.ValidateSettings())
	require.NoError(t, db.Create(&channel).Error)
	var loaded model.Channel
	require.NoError(t, db.First(&loaded, channel.Id).Error)
	require.NoError(t, loaded.ValidateSettings())
	assert.Equal(t, settings, loaded.GetSetting())

	updated := loaded.GetSetting()
	updated.ProtocolRouting.AccountResource = "supplier/account-b"
	updated.ProtocolRouting.QuotaScope = "account"
	updated.ProtocolRouting.Defaults.LossPolicy = "strict"
	override := updated.ProtocolRouting.Models["provider/model-x"]
	override.Endpoints[0].Path = "/vendor/v1/responses"
	override.Endpoints[0].Features = append(override.Endpoints[0].Features, "structured_output")
	updated.ProtocolRouting.Models["provider/model-x"] = override
	loaded.SetSetting(updated)
	require.NoError(t, loaded.ValidateSettings())
	require.NoError(t, db.Model(&model.Channel{}).Where("id = ?", loaded.Id).Update("setting", loaded.Setting).Error)
	var reloaded model.Channel
	require.NoError(t, db.First(&reloaded, loaded.Id).Error)
	require.NoError(t, reloaded.ValidateSettings())
	actual := reloaded.GetSetting()
	assert.Equal(t, updated, actual)
	assert.Equal(t, settings.Proxy, actual.Proxy)
	assert.Equal(t, settings.SystemPrompt, actual.SystemPrompt)
}

func TestParseQuotaResetAt(t *testing.T) {
	cases := []struct {
		name    string
		message string
		want    time.Time
		wantOK  bool
	}{
		{
			name:    "kimi 5-hour window",
			message: "status_code=429, You have exceeded the 5-hour usage quota. It will reset at 2026-09-10 13:59:10 +0800 CST. We recommend upgrading your plan for more quota, or waiting for the reset.",
			want:    time.Date(2026, 9, 10, 13, 59, 10, 0, time.FixedZone("", 8*3600)),
			wantOK:  true,
		},
		{
			name:    "volcengine weekly quota",
			message: "status_code=429, You have exceeded the weekly usage quota. It will reset at 2026-09-07 00:00:00 +0800 CST.",
			want:    time.Date(2026, 9, 7, 0, 0, 0, 0, time.FixedZone("", 8*3600)),
			wantOK:  true,
		},
		{
			name:    "relative reset wording without absolute time",
			message: "You've reached your 5-hour usage limit. Your quota will reset when the current 5-hour window ends.",
			wantOK:  false,
		},
		{
			name:    "no reset information",
			message: "status_code=429, inference exceeds tpm/rpm limit",
			wantOK:  false,
		},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parseQuotaResetAt(tt.message)
			require.Equal(t, tt.wantOK, ok)
			if tt.wantOK {
				assert.True(t, tt.want.Equal(got), "want %s, got %s", tt.want, got)
			}
		})
	}
}

func TestRoutingFailureQuotaCooldownFollowsResetTime(t *testing.T) {
	db := setupChannelSelectAutoGroupsTest(t)
	old := operation_setting.RoutingPolicyJSON()
	t.Cleanup(func() { require.NoError(t, operation_setting.UpdateRoutingPolicy(old)); routingCooldowns.Clear() })
	require.NoError(t, operation_setting.UpdateRoutingPolicy(`{"enabled":true,"group_tag_order":{"team":["plan"]},"quota_cooldown_seconds":3600,"quota_error_keywords":["usage quota"],"request_timeout_seconds":300}`))
	createChannelSelectAutoGroupsChannel(t, db, 3301, "team", "m")
	require.NoError(t, db.Model(&model.Channel{}).Where("id = ?", 3301).Update("tag", "plan").Error)
	model.InitChannelCache()
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest("POST", "/v1/messages", nil)

	msgWithResetAt := func(at time.Time) string {
		return fmt.Sprintf("You have exceeded the 5-hour usage quota. It will reset at %s CST.", at.UTC().Format("2006-01-02 15:04:05 -0700"))
	}

	// 重置时间 2 小时后：冷却跟随消息时间（+60s 余量），而非固定 3600s
	RecordRoutingFailure(ctx, 3301, "m2h", 429, msgWithResetAt(time.Now().Add(2*time.Hour)))
	v, ok := routingCooldowns.Load(routingCooldownKey{"channel:3301", "m2h"})
	require.True(t, ok)
	assert.WithinDuration(t, time.Now().Add(2*time.Hour).Add(time.Minute), v.(time.Time), 2*time.Minute)

	// 重置时间 30 天后：冷却封顶 6×基础冷却（6h）
	RecordRoutingFailure(ctx, 3301, "m30d", 429, msgWithResetAt(time.Now().Add(30*24*time.Hour)))
	v, ok = routingCooldowns.Load(routingCooldownKey{"channel:3301", "m30d"})
	require.True(t, ok)
	assert.WithinDuration(t, time.Now().Add(6*time.Hour), v.(time.Time), 2*time.Minute)

	// 重置时间已过（时钟偏差）：短冷却 60s
	RecordRoutingFailure(ctx, 3301, "mpast", 429, msgWithResetAt(time.Now().Add(-time.Hour)))
	v, ok = routingCooldowns.Load(routingCooldownKey{"channel:3301", "mpast"})
	require.True(t, ok)
	assert.WithinDuration(t, time.Now().Add(time.Minute), v.(time.Time), 30*time.Second)
}

func TestRoutingFailureAuthAndServerErrorCooldowns(t *testing.T) {
	db := setupChannelSelectAutoGroupsTest(t)
	old := operation_setting.RoutingPolicyJSON()
	t.Cleanup(func() { require.NoError(t, operation_setting.UpdateRoutingPolicy(old)); routingCooldowns.Clear() })
	require.NoError(t, operation_setting.UpdateRoutingPolicy(`{"enabled":true,"group_tag_order":{"team":["plan"]},"quota_cooldown_seconds":3600,"quota_error_keywords":["weekly"],"rate_limit_cooldown_seconds":60,"request_timeout_seconds":300}`))
	for _, id := range []int{3401, 3402} {
		createChannelSelectAutoGroupsChannel(t, db, id, "team", "m")
		require.NoError(t, db.Model(&model.Channel{}).Where("id = ?", id).Update("tag", "plan").Error)
	}
	model.InitChannelCache()
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest("POST", "/v1/messages", nil)

	// 401：账号级冷却 300s；整渠道禁用被连败门控（3 次才放行）
	RecordRoutingFailure(ctx, 3401, "m", 401, "invalid api key")
	accountKey1 := routingCooldownKey{"channel:3401", ""}
	v, ok := routingCooldowns.Load(accountKey1)
	require.True(t, ok, "401 must produce an account-level cooldown")
	assert.WithinDuration(t, time.Now().Add(300*time.Second), v.(time.Time), 30*time.Second)
	assert.False(t, AllowAutoBanChannel(ctx, 3401, 401))
	assert.False(t, AllowAutoBanChannel(ctx, 3401, 401))
	assert.True(t, AllowAutoBanChannel(ctx, 3401, 401), "连败 3 次才允许整渠道禁用")
	// 一次成功清零连败，重新计数
	RecordRoutingSuccess(ctx, 3401, "m")
	assert.False(t, AllowAutoBanChannel(ctx, 3401, 401), "成功后连败已清零")

	// 502（独立渠道避免被已有更长冷却覆盖）：账号级冷却 30s
	RecordRoutingFailure(ctx, 3402, "m", 502, "upstream Claude stream ended without message_stop")
	accountKey2 := routingCooldownKey{"channel:3402", ""}
	v, ok = routingCooldowns.Load(accountKey2)
	require.True(t, ok, "5xx must produce an account-level cooldown")
	assert.WithinDuration(t, time.Now().Add(30*time.Second), v.(time.Time), 15*time.Second)

	// 手动重新启用：冷却与连败残留全部清除
	ClearRoutingCooldownsForChannel(3401)
	_, ok = routingCooldowns.Load(accountKey1)
	assert.False(t, ok, "manual re-enable must clear residual cooldowns")
	_, ok = routingAuthStrikes.Load(accountKey1)
	assert.False(t, ok, "manual re-enable must clear auth strikes")
}

func TestRoutingBudget(t *testing.T) {
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	assert.False(t, RoutingBudgetExceeded(ctx), "未设置预算（非策略分组）永不超限")
	StartRoutingBudget(ctx, 60)
	assert.False(t, RoutingBudgetExceeded(ctx))
	common.SetContextKey(ctx, constant.ContextKeyRoutingBudgetDeadline, time.Now().Add(-time.Second))
	assert.True(t, RoutingBudgetExceeded(ctx), "过期预算必须报告超限")
}
