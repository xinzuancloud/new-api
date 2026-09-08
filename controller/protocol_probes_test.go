package controller

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/setting/protocol_setting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestProtocolProbeWorkflow(t *testing.T) {
	// Dedicated disposable databases only. The same contract runs on all engines.
	var dialector gorm.Dialector = sqlite.Open(filepath.Join(t.TempDir(), "profiles.db") + "?_pragma=busy_timeout(30000)&_pragma=journal_mode(WAL)&_txlock=immediate")
	switch os.Getenv("PROTOCOL_TEST_DIALECT") {
	case "mysql":
		dialector = mysql.Open(os.Getenv("PROTOCOL_TEST_DSN"))
	case "postgres":
		dialector = postgres.Open(os.Getenv("PROTOCOL_TEST_DSN"))
	}
	db, err := gorm.Open(dialector, &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(4)
	previous, cache := model.DB, common.MemoryCacheEnabled
	previousDialect := common.MainDatabaseType()
	common.SetMainDatabaseType(common.DatabaseType(db.Dialector.Name()))
	oldCatalog := protocol_setting.JSON(protocol_setting.Get())
	model.DB = db
	common.MemoryCacheEnabled = false
	common.OptionMapRWMutex.Lock()
	oldOptions := common.OptionMap
	common.OptionMap = map[string]string{}
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		model.DB = previous
		common.SetMainDatabaseType(previousDialect)
		common.MemoryCacheEnabled = cache
		require.NoError(t, protocol_setting.Update(oldCatalog))
		common.OptionMapRWMutex.Lock()
		common.OptionMap = oldOptions
		common.OptionMapRWMutex.Unlock()
		_ = sqlDB.Close()
	})
	require.NoError(t, db.Migrator().DropTable(&model.Option{}, &model.Channel{}, &model.Ability{}))
	require.NoError(t, db.AutoMigrate(&model.Option{}, &model.Channel{}, &model.Ability{}))
	require.NoError(t, db.AutoMigrate(&model.Option{}, &model.Channel{}, &model.Ability{}))
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		assert.Equal(t, "Bearer fixture-key", r.Header.Get("Authorization"))
		var body map[string]any
		require.NoError(t, common.DecodeJson(r.Body, &body))
		assert.Equal(t, "wire", body["model"])
		if body["stream"] == true {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"name\":\"newapi_probe\",\"arguments\":\"{\\\"value\\\":7}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n")
		} else {
			_, _ = fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"OK"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":1}}`)
		}
	}))
	defer server.Close()
	catalog, err := protocol_setting.Parse(`{"vendor":{"name":"Vendor","defaults":{"entry_formats":["openai","claude","openai_responses"],"endpoints":[{"format":"openai","path":"/v1/chat/completions","features":["reasoning"],"verified":false}]}}}`)
	require.NoError(t, err)
	revision, err := model.UpdateProtocolProfiles(protocol_setting.Revision(protocol_setting.Catalog{}), catalog)
	require.NoError(t, err)
	channel := model.Channel{Name: "account", Type: 1, Key: "fixture-key", Status: common.ChannelStatusEnabled, Models: "public", Group: "default", UsedQuota: 91, BaseURL: common.GetPointer(server.URL), ModelMapping: common.GetPointer(`{"public":"alias","alias":"wire"}`), Setting: common.GetPointer(`{"protocol_routing":{"profile":"vendor","enabled":true,"account_resource":"account"}}`)}
	require.NoError(t, channel.Insert())
	legacy := model.Channel{Name: "legacy", Type: 1, Status: common.ChannelStatusEnabled, Setting: common.GetPointer("broken")}
	require.NoError(t, db.Create(&legacy).Error)
	request := protocolProbePlanRequest{Profile: "vendor", Checks: []string{"text", "tools"}}
	_, err = planProtocolProbes(protocolProbePlanRequest{Profile: "vendor", ChannelIDs: []int{legacy.Id}, Checks: []string{"text"}})
	require.EqualError(t, err, "invalid_channel_settings")
	require.NoError(t, db.Model(&legacy).Update("setting", `{"protocol_routing":{"profile":"vendor","enabled":"invalid"}}`).Error)
	_, err = planProtocolProbes(request)
	require.EqualError(t, err, "invalid_channel_settings", "an identifiable binding must still receive strict validation")
	require.NoError(t, db.Model(&legacy).Update("setting", "broken").Error)
	report, err := planProtocolProbes(request)
	require.NoError(t, err)
	require.Len(t, report.Cases, 2)
	assert.Equal(t, "wire", report.Cases[0].Model)
	assert.Equal(t, channel.Id, report.Cases[0].ChannelID)
	require.NoError(t, model.CreateProtocolProbeReport(report))
	stored, err := model.ReadProtocolProbeReport(nil, report.ID, false)
	require.NoError(t, err)
	assert.Equal(t, "pending", stored.Status)
	// Two transactions claiming the same report must not both start paid work.
	startClaims := make(chan struct{})
	claimResults := make(chan error, 2)
	var claims sync.WaitGroup
	for range 2 {
		claims.Add(1)
		go func() {
			defer claims.Done()
			<-startClaims
			_, e := model.MutateProtocolProbeReport(report.ID, func(saved *model.ProtocolProbeReport) error {
				if saved.RunID != "" {
					return model.ErrProtocolConflict
				}
				saved.RunID = "claimed"
				saved.LeaseUntil = time.Now().Add(time.Minute).Unix()
				return nil
			})
			claimResults <- e
		}()
	}
	close(startClaims)
	claims.Wait()
	close(claimResults)
	successfulClaims := 0
	for e := range claimResults {
		if e == nil {
			successfulClaims++
		} else {
			require.ErrorIs(t, e, model.ErrProtocolConflict)
		}
	}
	assert.Equal(t, 1, successfulClaims)
	_, err = model.MutateProtocolProbeReport(report.ID, func(saved *model.ProtocolProbeReport) error { saved.RunID = ""; saved.LeaseUntil = 0; return nil })
	require.NoError(t, err)

	invoke := func(handler gin.HandlerFunc, payload any) *httptest.ResponseRecorder {
		data, err := common.Marshal(payload)
		require.NoError(t, err)
		recorder := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(recorder)
		ctx.Params = gin.Params{{Key: "id", Value: report.ID}}
		ctx.Request = httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(data))
		handler(ctx)
		return recorder
	}
	response := invoke(RunProtocolProbe, nil)
	require.Equal(t, 200, response.Code, response.Body.String())
	assert.Equal(t, int32(2), calls.Load())
	stored, err = model.ReadProtocolProbeReport(nil, report.ID, false)
	require.NoError(t, err)
	assert.Equal(t, "completed", stored.Status)
	for _, test := range stored.Cases {
		assert.Equal(t, "passed", test.Result.Outcome, test.Result.Reason)
	}
	// No upstream response text or credential can escape in report JSON.
	data, err := common.Marshal(stored)
	require.NoError(t, err)
	assert.NotContains(t, string(data), "fixture-key")
	assert.NotContains(t, string(data), "Bearer")
	assert.NotContains(t, string(data), "run_id")
	response = invoke(ApplyProtocolProbe, map[string]any{"revision": revision, "preview": true})
	require.Equal(t, 200, response.Code, response.Body.String())
	var preview struct {
		Data struct {
			Fingerprint   string `json:"fingerprint"`
			Before, After protocol_setting.Profile
		}
	}
	require.NoError(t, common.Unmarshal(response.Body.Bytes(), &preview))
	assert.Empty(t, preview.Data.Before.Models)
	require.NotEmpty(t, preview.Data.After.Models)
	response = invoke(ApplyProtocolProbe, map[string]any{"revision": revision, "fingerprint": preview.Data.Fingerprint})
	require.Equal(t, 200, response.Code, response.Body.String())
	current, err := model.ReadProtocolProfiles()
	require.NoError(t, err)
	policy, err := current.Resolve(&dto.ProtocolRoutingSettings{Profile: "vendor", Enabled: true}, "wire")
	require.NoError(t, err)
	assert.True(t, policy.Endpoints[0].Verified)
	assert.ElementsMatch(t, []string{"reasoning", "stream", "tools"}, policy.Endpoints[0].Features)
	response = invoke(ApplyProtocolProbe, map[string]any{"revision": "old"})
	require.Equal(t, 200, response.Code, "repeat application is idempotent")
	require.NoError(t, db.First(&channel, channel.Id).Error)
	assert.EqualValues(t, 91, channel.UsedQuota)
	assert.Equal(t, "default", channel.Group)
	// Changed credential invalidates a saved plan before any new upstream calls.
	report, err = planProtocolProbes(request)
	require.NoError(t, err)
	require.NoError(t, model.CreateProtocolProbeReport(report))
	require.NoError(t, db.Model(&model.Channel{}).Where("id = ?", channel.Id).Update("key", "rotated-key").Error)
	response = invoke(RunProtocolProbe, nil)
	assert.Equal(t, 409, response.Code)
	assert.Equal(t, int32(2), calls.Load())
	require.NoError(t, db.Model(&model.Channel{}).Where("id = ?", channel.Id).Update("key", "fixture-key").Error)
	// An abandoned lease is not replayed; its cases become unknown.
	_, err = model.MutateProtocolProbeReport(report.ID, func(saved *model.ProtocolProbeReport) error {
		saved.RunID = "abandoned"
		saved.LeaseUntil = time.Now().Unix() - 1
		saved.Status = "running"
		for i := range saved.Cases {
			saved.Cases[i].Result.Outcome = "running"
		}
		return nil
	})
	require.NoError(t, err)
	response = invoke(RunProtocolProbe, nil)
	assert.Equal(t, 200, response.Code)
	assert.Equal(t, int32(2), calls.Load())
	stored, err = model.ReadProtocolProbeReport(nil, report.ID, false)
	require.NoError(t, err)
	for _, test := range stored.Cases {
		assert.Equal(t, "interrupted_batch", test.Result.Reason)
	}
	report, err = planProtocolProbes(request)
	require.NoError(t, err)
	require.NoError(t, model.CreateProtocolProbeReport(report))
	response = invoke(CancelProtocolProbe, nil)
	assert.Equal(t, 200, response.Code)
	response = invoke(RunProtocolProbe, nil)
	assert.Equal(t, 409, response.Code)
	assert.Equal(t, int32(2), calls.Load())
	_, err = planProtocolProbes(protocolProbePlanRequest{Profile: "vendor", Checks: []string{"web_search"}})
	require.Error(t, err)
	// The atomic catalog callback cannot leave a report applied if profile validation fails.
	current, err = model.ReadProtocolProfiles()
	require.NoError(t, err)
	_, err = model.MutateProtocolProfiles(protocol_setting.Revision(current), func(tx *gorm.DB, c protocol_setting.Catalog) error {
		saved, e := model.ReadProtocolProbeReport(tx, report.ID, true)
		if e != nil {
			return e
		}
		saved.Status = "applied"
		if e = model.SaveProtocolProbeReport(tx, saved); e != nil {
			return e
		}
		delete(c, "vendor")
		return nil
	})
	require.Error(t, err)
	stored, err = model.ReadProtocolProbeReport(nil, report.ID, false)
	require.NoError(t, err)
	assert.Equal(t, "cancelled", stored.Status)

	// Cancelled crash recovery retains earlier paid evidence and permits preview.
	_, err = model.MutateProtocolProbeReport(report.ID, func(saved *model.ProtocolProbeReport) error {
		saved.RunID = "abandoned"
		saved.LeaseUntil = time.Now().Unix() - 1
		saved.Cases[0].Result = model.ProtocolProbeResult{Outcome: "passed", Reason: "verified_fixture", Terminal: true}
		saved.Cases[1].Result.Outcome = "running"
		return nil
	})
	require.NoError(t, err)
	response = invoke(ApplyProtocolProbe, map[string]any{"revision": protocol_setting.Revision(current), "preview": true})
	require.Equal(t, 200, response.Code, response.Body.String())
	stored, err = model.ReadProtocolProbeReport(nil, report.ID, false)
	require.NoError(t, err)
	assert.Empty(t, stored.RunID)
	assert.Equal(t, "passed", stored.Cases[0].Result.Outcome)
	assert.Equal(t, "interrupted_batch", stored.Cases[1].Result.Reason)
	// Reject an oversized future result before issuing any provider calls.
	oversized := &model.ProtocolProbeReport{Profile: "vendor", ProfileRevision: strings.Repeat("a", 64), Fingerprints: map[int]string{1: strings.Repeat("b", 64)}, MaxOutputTokens: 256}
	for i := range 128 {
		oversized.Cases = append(oversized.Cases, model.ProtocolProbeCase{ID: i + 1, ChannelID: 1, Model: strings.Repeat("m", 220), Endpoint: dto.ProtocolEndpoint{Format: "openai", Path: "/v1/chat/completions"}, Check: "tools", Result: model.ProtocolProbeResult{Outcome: "pending", Reason: "not_run"}})
	}
	require.Error(t, model.CreateProtocolProbeReport(oversized))
	// Binding previews and direct channel writes share the same catalog contract
	// on SQLite, MySQL and PostgreSQL.
	rev := protocol_setting.Revision(current)
	_, err = model.UpdateProtocolProfiles("stale", current)
	require.ErrorIs(t, err, model.ErrProtocolConflict)
	invalid := model.Channel{Id: channel.Id, Setting: common.GetPointer(`{"protocol_routing":{"enabled":true,"profile":"missing"}}`)}
	require.Error(t, invalid.Update())
	binding, err := model.BindProtocolChannels(rev, "", []int{channel.Id}, true, "")
	require.NoError(t, err)
	assert.Equal(t, binding.Changes[0].Before, binding.Changes[0].After)
	_, err = model.BindProtocolChannels(rev, "", []int{channel.Id}, false, "stale")
	require.ErrorIs(t, err, model.ErrProtocolConflict)
	_, err = model.BindProtocolChannels(rev, "", []int{channel.Id}, false, binding.Fingerprint)
	require.NoError(t, err)
	require.NoError(t, db.First(&channel, channel.Id).Error)
	assert.Equal(t, "fixture-key", channel.Key)
	assert.EqualValues(t, 91, channel.UsedQuota)
	_, err = model.UpdateProtocolProfiles(rev, protocol_setting.Catalog{})
	require.NoError(t, err)

	// Retention keeps the leased report even when it is older than all others.
	_, err = model.MutateProtocolProbeReport(report.ID, func(saved *model.ProtocolProbeReport) error {
		saved.CreatedAt = 1
		saved.RunID = "active"
		saved.LeaseUntil = time.Now().Add(time.Minute).Unix()
		return nil
	})
	require.NoError(t, err)
	for range 20 {
		candidate := &model.ProtocolProbeReport{Profile: "vendor", ProfileRevision: strings.Repeat("a", 64), Fingerprints: map[int]string{}, MaxOutputTokens: 256, Cases: []model.ProtocolProbeCase{{ID: 1, ChannelID: 1, Model: "model", Endpoint: dto.ProtocolEndpoint{Format: "openai", Path: "/v1/chat/completions"}, Check: "text", Result: model.ProtocolProbeResult{Outcome: "pending", Reason: "not_run"}}}}
		require.NoError(t, model.CreateProtocolProbeReport(candidate))
	}
	reports, err := model.ListProtocolProbeReports()
	require.NoError(t, err)
	assert.Len(t, reports, 20)
	stored, err = model.ReadProtocolProbeReport(nil, report.ID, false)
	require.NoError(t, err)
	assert.Equal(t, "active", stored.RunID)

}
