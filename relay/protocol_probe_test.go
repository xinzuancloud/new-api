package relay

import (
	"context"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestProtocolProbeEvidence(t *testing.T) {
	for _, tc := range []struct {
		name    string
		format  types.RelayFormat
		check   string
		status  int
		body    string
		outcome string
	}{
		{"empty stream", types.RelayFormatClaude, "stream", 200, "", "unknown"},
		{"rate limit", types.RelayFormatClaude, "text", 429, `{"error":{"message":"secret"}}`, "unknown"},
		{"rejected fixture", types.RelayFormatClaude, "tools", 400, `{"error":{"message":"secret"}}`, "rejected"},
		{"ignored tool", types.RelayFormatOpenAI, "tools", 200, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n", "unknown"},
		{"truncated SSE", types.RelayFormatClaude, "stream", 200, "data: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"ok\"}}\n\n", "unknown"},
		{"message", types.RelayFormatClaude, "text", 200, `{"type":"message","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":2}}`, "passed"},
		{"responses incomplete", types.RelayFormatOpenAIResponses, "text", 200, `{"status":"incomplete","output":[{"type":"message","content":[{"type":"output_text","text":"ok"}]}]}`, "unknown"},
		{"forged text tool", types.RelayFormatClaude, "tools", 200, "data: {\"type\":\"content_block_delta\",\"delta\":{\"text\":\"newapi_probe({value:7})\"}}\n\ndata: {\"type\":\"message_stop\"}\n\n", "unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := service.EvaluateProtocolProbe(tc.format, tc.check, tc.status, []byte(tc.body))
			assert.Equal(t, tc.outcome, result.Outcome)
			data, err := common.Marshal(result)
			require.NoError(t, err)
			assert.NotContains(t, string(data), "secret")
		})
	}
}

func TestProtocolProbeFixtureBounds(t *testing.T) {
	for _, format := range []types.RelayFormat{types.RelayFormatOpenAI, types.RelayFormatClaude, types.RelayFormatOpenAIResponses} {
		for _, check := range []string{"text", "stream", "tools", "images", "web_search"} {
			raw, stream, err := service.BuildProtocolProbeFixture(format, "model", check, 256)
			require.NoError(t, err)
			var body map[string]any
			require.NoError(t, common.Unmarshal(raw, &body))
			assert.Equal(t, "model", body["model"])
			assert.Equal(t, check == "stream" || check == "tools" || check == "web_search", stream)
		}
	}
	_, _, err := service.BuildProtocolProbeFixture(types.RelayFormatClaude, "model", "text", 1025)
	require.Error(t, err)
	_, _, err = service.BuildProtocolProbeFixture(types.RelayFormatClaude, "model", "namespaces", 256)
	require.Error(t, err)
}

func TestProtocolProbeNativeToolEvidence(t *testing.T) {
	for _, tc := range []struct {
		format      types.RelayFormat
		check, body string
	}{
		{types.RelayFormatClaude, "tools", "data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"tool_use\",\"name\":\"newapi_probe\",\"input\":{}}}\n\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\\\"value\\\":7}\"}}\n\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\"}}\n\ndata: {\"type\":\"message_stop\"}\n\n"},
		{types.RelayFormatOpenAIResponses, "namespaces", "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"output\":[{\"type\":\"function_call\",\"id\":\"call\",\"namespace\":\"probe_namespace\",\"name\":\"newapi_probe\",\"arguments\":\"{\\\"value\\\":7}\"}]}}\n\n"},
		{types.RelayFormatClaude, "web_search", "data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"server_tool_use\",\"name\":\"web_search\"}}\n\ndata: {\"type\":\"content_block_start\",\"index\":1,\"content_block\":{\"type\":\"web_search_tool_result\",\"content\":[{\"type\":\"web_search_result\",\"url\":\"https://go.dev\"}]}}\n\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"}}\n\ndata: {\"type\":\"message_stop\"}\n\n"},
		{types.RelayFormatOpenAIResponses, "web_search", "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"output\":[{\"type\":\"web_search_call\",\"status\":\"completed\",\"action\":{\"sources\":[{\"url\":\"https://go.dev\"}]}}]}}\n\n"},
	} {
		t.Run(string(tc.format)+tc.check, func(t *testing.T) {
			result := service.EvaluateProtocolProbe(tc.format, tc.check, 200, []byte(tc.body))
			assert.Equal(t, "passed", result.Outcome)
			assert.True(t, result.Terminal)
		})
	}
}

func TestProtocolProbeTransportBoundaries(t *testing.T) {
	service.InitHttpClient()
	var escaped atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { escaped.Add(1) }))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", target.URL)
		w.WriteHeader(http.StatusTemporaryRedirect)
	}))
	defer source.Close()
	upstream := model.Channel{Type: 1, Key: "private-fixture", BaseURL: common.GetPointer(source.URL)}
	test := model.ProtocolProbeCase{Model: "model", Check: "text", Endpoint: dto.ProtocolEndpoint{Format: types.RelayFormatOpenAI, Path: "/v1/chat/completions"}}
	result := RunProtocolProbe(context.Background(), &upstream, test, 256)
	assert.Equal(t, 307, result.HTTPStatus)
	assert.EqualValues(t, 0, escaped.Load())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result = RunProtocolProbe(ctx, &upstream, test, 256)
	assert.Equal(t, "cancelled", result.Outcome)
	upstream.OtherSettings = "broken"
	require.NotPanics(t, func() { result = RunProtocolProbe(context.Background(), &upstream, test, 256) })
	assert.Equal(t, "configuration_unavailable", result.Reason)
	assert.Equal(t, "broken", upstream.OtherSettings, "diagnostics must not repair persisted settings")

}

func TestProtocolProbeFingerprintTracksAccountSelection(t *testing.T) {
	channel := model.Channel{Id: 1, Type: 1, Key: "first\nsecond", ChannelInfo: model.ChannelInfo{IsMultiKey: true, MultiKeyStatusList: map[int]int{0: common.ChannelStatusEnabled, 1: common.ChannelStatusEnabled}}}
	before := model.ProtocolProbeFingerprint(&channel)
	channel.ChannelInfo.MultiKeyStatusList[0] = common.ChannelStatusManuallyDisabled
	assert.NotEqual(t, before, model.ProtocolProbeFingerprint(&channel))
}
