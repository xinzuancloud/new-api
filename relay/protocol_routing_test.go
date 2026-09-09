package relay

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProtocolBridgeRoundTripsSixDirections(t *testing.T) {
	originalForce := constant.ForceStreamOption
	constant.ForceStreamOption = true
	t.Cleanup(func() { constant.ForceStreamOption = originalForce })
	originalTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = originalTimeout })
	requests := map[types.RelayFormat]string{
		types.RelayFormatOpenAI:          `{"model":"public-model","messages":[{"role":"user","content":"hello"}],"max_tokens":32}`,
		types.RelayFormatClaude:          `{"model":"public-model","messages":[{"role":"user","content":"hello"}],"max_tokens":32}`,
		types.RelayFormatOpenAIResponses: `{"model":"public-model","input":"hello","max_output_tokens":32}`,
	}
	responses := map[types.RelayFormat]string{
		types.RelayFormatOpenAI:          `{"id":"chatcmpl_fixture","object":"chat.completion","model":"upstream-model","choices":[{"index":0,"message":{"role":"assistant","content":"ok","tool_calls":[{"id":"call_1","type":"function","function":{"name":"weather","arguments":"{\"city\":\"Paris\"}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":5,"completion_tokens":3,"total_tokens":8}}`,
		types.RelayFormatClaude:          `{"id":"msg_fixture","type":"message","role":"assistant","model":"upstream-model","content":[{"type":"text","text":"ok"},{"type":"tool_use","id":"call_1","name":"weather","input":{"city":"Paris"}}],"stop_reason":"tool_use","usage":{"input_tokens":5,"output_tokens":3}}`,
		types.RelayFormatOpenAIResponses: `{"id":"resp_fixture","object":"response","status":"completed","model":"upstream-model","output":[{"type":"message","id":"msg_1","role":"assistant","content":[{"type":"output_text","text":"ok"}]},{"type":"function_call","call_id":"call_1","name":"weather","arguments":"{\"city\":\"Paris\"}"}],"usage":{"input_tokens":5,"output_tokens":3,"total_tokens":8}}`,
	}
	formats := []types.RelayFormat{types.RelayFormatOpenAI, types.RelayFormatClaude, types.RelayFormatOpenAIResponses}
	for _, streaming := range []bool{false, true} {
		for _, source := range formats {
			for _, target := range formats {
				if source == target {
					continue
				}
				t.Run(fmt.Sprintf("%s_to_%s_stream_%t", source, target, streaming), func(t *testing.T) {
					var outbound map[string]any
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						assert.Equal(t, "/provider/text", r.URL.Path)
						assert.Equal(t, "Bearer fixture-key", r.Header.Get("Authorization"))
						raw, err := io.ReadAll(r.Body)
						require.NoError(t, err)
						require.NoError(t, common.Unmarshal(raw, &outbound))
						if streaming && target == types.RelayFormatOpenAI {
							options, ok := outbound["stream_options"].(map[string]any)
							require.True(t, ok)
							assert.Equal(t, true, options["include_usage"])
						}
						if streaming {
							w.Header().Set("Content-Type", "text/event-stream")
							_, _ = w.Write([]byte(protocolStreamFixture(target, responses[target])))
						} else {
							w.Header().Set("Content-Type", "application/json")
							_, _ = w.Write([]byte(responses[target]))
						}
					}))
					defer server.Close()
					rawRequest := requests[source]
					if streaming {
						rawRequest = strings.TrimSuffix(rawRequest, "}") + `,"stream":true}`
					}
					c, info := protocolTestContext(t, source, rawRequest, server.URL, target)
					plan, apiErr := PrepareProtocolRequest(c, info)
					require.Nil(t, apiErr)
					require.NotNil(t, plan)
					usage, apiErr := sendProtocolRequest(c, info, plan)
					require.Nil(t, apiErr)
					require.NotNil(t, usage)
					assert.Equal(t, "upstream-model", outbound["model"])
					assert.Equal(t, 5, usage.PromptTokens)
					assert.Equal(t, 3, usage.CompletionTokens)
					body := c.Writer.(*protocolTestWriter).recorder.Body.String()
					assert.Contains(t, body, "weather")
					assert.Contains(t, body, "Paris")
					if streaming {
						switch source {
						case types.RelayFormatOpenAI:
							assert.Contains(t, body, "[DONE]")
						case types.RelayFormatClaude:
							assert.Contains(t, body, "message_stop")
						case types.RelayFormatOpenAIResponses:
							assert.Contains(t, body, "response.completed")
						}
					}
					switch source {
					case types.RelayFormatOpenAI:
						assert.Contains(t, body, "tool_calls")
					case types.RelayFormatClaude:
						assert.Contains(t, body, "tool_use")
					case types.RelayFormatOpenAIResponses:
						assert.Contains(t, body, "function_call")
					}
				})
			}
		}
	}
}

type protocolTestWriter struct {
	gin.ResponseWriter
	recorder *httptest.ResponseRecorder
}

func protocolTestContext(t *testing.T, source types.RelayFormat, raw, base string, target types.RelayFormat) (*gin.Context, *relaycommon.RelayInfo) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Writer = &protocolTestWriter{c.Writer, recorder}
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewBufferString(raw))
	c.Request.Header.Set("Content-Type", "application/json")
	var request dto.Request
	switch source {
	case types.RelayFormatOpenAI:
		request = &dto.GeneralOpenAIRequest{}
	case types.RelayFormatClaude:
		request = &dto.ClaudeRequest{}
	default:
		request = &dto.OpenAIResponsesRequest{}
	}
	require.NoError(t, common.Unmarshal([]byte(raw), request))
	common.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeMoonshot)
	common.SetContextKey(c, constant.ContextKeyChannelId, 1)
	common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, base)
	common.SetContextKey(c, constant.ContextKeyChannelKey, "fixture-key")
	common.SetContextKey(c, constant.ContextKeyOriginalModel, "public-model")
	c.Set("model_mapping", `{"public-model":"upstream-model"}`)
	common.SetContextKey(c, constant.ContextKeyChannelSetting, dto.ChannelSettings{ProtocolRouting: &dto.ProtocolRoutingSettings{Enabled: true, Defaults: dto.ProtocolModelPolicy{EntryFormats: []types.RelayFormat{source}, LossPolicy: "safe", Endpoints: []dto.ProtocolEndpoint{{Format: target, Path: "/provider/text", Verified: true, Features: []string{"tools", "stream"}}}}}})
	info := &relaycommon.RelayInfo{Request: request, IsStream: request.IsStream(c.Request), RelayFormat: source, OriginModelName: "public-model", RequestURLPath: "/v1/responses"}
	t.Cleanup(func() { common.CleanupBodyStorage(c) })
	return c, info
}

func TestProtocolPreflightRejectsStateBeforeSending(t *testing.T) {
	c, info := protocolTestContext(t, types.RelayFormatOpenAIResponses, `{"model":"public-model","input":"hello","previous_response_id":"resp_prior"}`, "https://example.invalid", types.RelayFormatClaude)
	plan, apiErr := PrepareProtocolRequest(c, info)
	assert.Nil(t, plan)
	require.NotNil(t, apiErr)
	assert.Equal(t, http.StatusBadRequest, apiErr.StatusCode)
	assert.Empty(t, c.GetStringSlice("use_channel"))
}

func protocolStreamFixture(format types.RelayFormat, full string) string {
	switch format {
	case types.RelayFormatOpenAI:
		return "data: " + `{"id":"chatcmpl_fixture","model":"upstream-model","choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"weather","arguments":"{\"city\":\"Paris\"}"}}]},"finish_reason":null}]}` + "\n\ndata: " + `{"id":"chatcmpl_fixture","model":"upstream-model","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":5,"completion_tokens":3,"total_tokens":8}}` + "\n\ndata: [DONE]\n\n"
	case types.RelayFormatClaude:
		return "event: message_start\ndata: " + `{"type":"message_start","message":{"id":"msg_fixture","type":"message","role":"assistant","model":"upstream-model","content":[],"usage":{"input_tokens":5,"output_tokens":0}}}` + "\n\nevent: content_block_start\ndata: " + `{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"call_1","name":"weather","input":{}}}` + "\n\nevent: content_block_delta\ndata: " + `{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"city\":\"Paris\"}"}}` + "\n\nevent: content_block_stop\ndata: " + `{"type":"content_block_stop","index":0}` + "\n\nevent: message_delta\ndata: " + `{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":3}}` + "\n\nevent: message_stop\ndata: " + `{"type":"message_stop"}` + "\n\n"
	default:
		full = `{"id":"resp_fixture","object":"response","status":"completed","model":"upstream-model","output":[{"type":"function_call","id":"fc_1","call_id":"call_1","name":"weather","arguments":"{\"city\":\"Paris\"}"}],"usage":{"input_tokens":5,"output_tokens":3,"total_tokens":8}}`
		return "event: response.created\ndata: " + `{"type":"response.created","response":{"id":"resp_fixture","object":"response","model":"upstream-model","status":"in_progress","output":[]}}` + "\n\nevent: response.output_item.added\ndata: " + `{"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","id":"fc_1","call_id":"call_1","name":"weather","arguments":""}}` + "\n\nevent: response.function_call_arguments.delta\ndata: " + `{"type":"response.function_call_arguments.delta","output_index":0,"item_id":"fc_1","delta":"{\"city\":\"Paris\"}"}` + "\n\nevent: response.completed\ndata: " + `{"type":"response.completed","response":` + full + "}\n\n"
	}
}

func TestProtocolEndpointUsesNativePlanBase(t *testing.T) {
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "kimi-coding-plan"}}
	for _, test := range []struct {
		format     types.RelayFormat
		path, want string
	}{
		{types.RelayFormatOpenAI, "/chat/completions", "https://api.kimi.com/coding/v1/chat/completions"},
		{types.RelayFormatClaude, "/v1/messages", "https://api.kimi.com/coding/v1/messages"},
	} {
		adapter := &protocolEndpointAdaptor{endpoint: dto.ProtocolEndpoint{Format: test.format, Path: test.path}}
		got, err := adapter.GetRequestURL(info)
		require.NoError(t, err)
		assert.Equal(t, test.want, got)
	}
}

func TestProtocolRoutingLeavesOtherRelayAPIsOnLegacyPath(t *testing.T) {
	c, info := protocolTestContext(t, types.RelayFormatOpenAI, `{"model":"public-model","messages":[{"role":"user","content":"hello"}]}`, "https://example.invalid", types.RelayFormatOpenAI)
	info.RelayFormat = types.RelayFormatGemini
	plan, err := PrepareProtocolRequest(c, info)
	assert.Nil(t, plan)
	assert.Nil(t, err)
}

func TestNativeClaudeContextEditingIsPreserved(t *testing.T) {
	for _, contextManagement := range []string{`{}`, `{"edits":[{"type":"clear_thinking_20251015","keep":"all"}]}`} {
		c, info := protocolTestContext(t, types.RelayFormatClaude, `{"model":"public-model","messages":[{"role":"user","content":"hello"}],"max_tokens":32,"context_management":`+contextManagement+`}`, "https://example.invalid", types.RelayFormatClaude)
		settings, _ := common.GetContextKeyType[dto.ChannelSettings](c, constant.ContextKeyChannelSetting)
		settings.ProtocolRouting.Defaults.Endpoints[0].Features = append(settings.ProtocolRouting.Defaults.Endpoints[0].Features, "context_editing")
		plan, apiErr := PrepareProtocolRequest(c, info)
		require.Nil(t, apiErr)
		require.NotNil(t, plan)
		var sent, want map[string]any
		require.NoError(t, common.Unmarshal(plan.Body, &sent))
		require.NoError(t, common.Unmarshal([]byte(contextManagement), &want))
		assert.Equal(t, want, sent["context_management"])
	}
}

func TestNativeProtocolRequestPreservesUnknownFields(t *testing.T) {
	c, info := protocolTestContext(t, types.RelayFormatClaude, `{"model":"public-model","messages":[{"role":"user","content":"hello"}],"max_tokens":32,"vendor_future":{"enabled":true}}`, "https://example.invalid", types.RelayFormatClaude)
	plan, apiErr := PrepareProtocolRequest(c, info)
	require.Nil(t, apiErr)
	require.NotNil(t, plan)
	var sent map[string]any
	require.NoError(t, common.Unmarshal(plan.Body, &sent))
	assert.Equal(t, map[string]any{"enabled": true}, sent["vendor_future"])
	assert.Equal(t, "upstream-model", sent["model"])
}

func TestNativeClaudeEmptyStreamRetriesSameEndpointOnce(t *testing.T) {
	originalTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = originalTimeout })
	var attempts int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.Header().Set("Content-Type", "text/event-stream")
		if attempts == 1 {
			return
		}
		_, _ = w.Write([]byte(protocolStreamFixture(types.RelayFormatClaude, "")))
	}))
	defer server.Close()
	c, info := protocolTestContext(t, types.RelayFormatClaude, `{"model":"public-model","messages":[{"role":"user","content":"hello"}],"max_tokens":32,"stream":true}`, server.URL, types.RelayFormatClaude)
	plan, apiErr := PrepareProtocolRequest(c, info)
	require.Nil(t, apiErr)

	usage, apiErr := sendProtocolRequest(c, info, plan)
	require.Nil(t, apiErr)
	require.NotNil(t, usage)
	assert.Equal(t, 2, attempts)
	assert.Contains(t, c.Writer.(*protocolTestWriter).recorder.Body.String(), "message_stop")
}

func TestNativeClaudeEmptyStreamRetryIsCappedAcrossChannels(t *testing.T) {
	originalTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = originalTimeout })
	var attempts int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.Header().Set("Content-Type", "text/event-stream")
	}))
	defer server.Close()
	c, info := protocolTestContext(t, types.RelayFormatClaude, `{"model":"public-model","messages":[{"role":"user","content":"hello"}],"max_tokens":32,"stream":true}`, server.URL, types.RelayFormatClaude)
	common.SetContextKey(c, constant.ContextKeyProtocolNativeEmptyStreamRetries, 1)
	plan, apiErr := PrepareProtocolRequest(c, info)
	require.Nil(t, apiErr)

	_, apiErr = sendProtocolRequest(c, info, plan)
	require.NotNil(t, apiErr)
	assert.Equal(t, 1, attempts)
	assert.Equal(t, 1, common.GetContextKeyInt(c, constant.ContextKeyProtocolNativeEmptyStreamRetries))
}
