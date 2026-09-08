package service

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProtocolCandidates(t *testing.T) {
	policy := &dto.ProtocolRoutingSettings{Enabled: true, Defaults: dto.ProtocolModelPolicy{EntryFormats: []types.RelayFormat{types.RelayFormatOpenAI, types.RelayFormatOpenAIResponses}, Endpoints: []dto.ProtocolEndpoint{
		{Format: types.RelayFormatClaude, Path: "/v1/messages", Verified: true},
		{Format: types.RelayFormatOpenAI, Path: "/v1/chat/completions", Verified: true},
		{Format: types.RelayFormatOpenAIResponses, Path: "/v1/responses"},
	}}}
	candidates, err := BuildProtocolCandidates(policy, "model", types.RelayFormatOpenAI, map[string]any{"messages": []any{map[string]any{"role": "user", "content": "private prompt"}}})
	require.NoError(t, err)
	require.Len(t, candidates, 2)
	assert.Equal(t, types.RelayFormatOpenAI, candidates[0].Endpoint.Format)
	assert.Equal(t, "safe", candidates[0].LossPolicy)
	assert.Equal(t, types.RelayFormat(types.RelayFormatClaude), candidates[1].Endpoint.Format)
	for _, request := range []map[string]any{
		{"stream": true}, {"tools": []any{map[string]any{"type": "function"}}},
		{"previous_response_id": "secret-response"}, {"background": true},
		{"messages": []any{map[string]any{"content": []any{map[string]any{"type": "image_url", "image_url": "secret-image"}}}}},
	} {
		candidates, err = BuildProtocolCandidates(policy, "model", types.RelayFormatOpenAIResponses, request)
		require.Error(t, err)
		assert.Empty(t, candidates)
		assert.NotContains(t, err.Error(), "secret")
	}
	policy.Models = map[string]dto.ProtocolModelPolicy{"mapped": {EntryFormats: []types.RelayFormat{types.RelayFormatClaude}, Endpoints: policy.Defaults.Endpoints}}
	_, err = BuildProtocolCandidates(policy, "mapped", types.RelayFormatOpenAI, map[string]any{})
	require.Error(t, err)
}

func TestProtocolCapabilityDiagnostics(t *testing.T) {
	policy := &dto.ProtocolRoutingSettings{Enabled: true, Defaults: dto.ProtocolModelPolicy{
		EntryFormats: []types.RelayFormat{types.RelayFormatOpenAIResponses},
		Endpoints: []dto.ProtocolEndpoint{
			{Format: types.RelayFormatOpenAI, Path: "/private-endpoint", Verified: true, Features: []string{"tools"}},
			{Format: types.RelayFormatClaude, Path: "/messages", Verified: true, Features: []string{"tools", "images"}},
		},
	}}
	request := map[string]any{
		"tools": []any{map[string]any{"type": "web_search", "description": "private tool text"}},
		"input": []any{map[string]any{"type": "input_image", "image_url": "private image"}},
	}
	candidates, err := BuildProtocolCandidates(policy, "private-model", types.RelayFormatOpenAIResponses, request)
	require.Error(t, err)
	assert.Empty(t, candidates)
	assert.EqualError(t, err, "no verified protocol endpoint supports the request capabilities; openai: missing capabilities: hosted_tools, images; claude: missing capabilities: hosted_tools")
	assert.NotContains(t, err.Error(), "private")
	policy.Defaults.Endpoints[1].Features = append(policy.Defaults.Endpoints[1].Features, "hosted_tools")
	candidates, err = BuildProtocolCandidates(policy, "private-model", types.RelayFormatOpenAIResponses, request)
	require.NoError(t, err)
	require.Len(t, candidates, 1)
	assert.Equal(t, types.RelayFormat(types.RelayFormatClaude), candidates[0].Endpoint.Format)
	for i := range policy.Defaults.Endpoints {
		policy.Defaults.Endpoints[i].Verified = false
	}
	_, err = BuildProtocolCandidates(policy, "private-model", types.RelayFormatOpenAIResponses, request)
	assert.EqualError(t, err, "no verified protocol endpoint supports the request capabilities; no endpoints are verified")
}

func TestProtocolSettingsValidation(t *testing.T) {
	for _, path := range []string{"https://other.test/v1", "//other.test/v1", "/v1?api_key=secret", "/v1#fragment", "/v1/../secret", "/v1/%2e%2e/secret", "/v1\\secret", "/v1\nsecret"} {
		t.Run(path, func(t *testing.T) {
			policy := dto.ProtocolRoutingSettings{Enabled: true, Defaults: dto.ProtocolModelPolicy{EntryFormats: []types.RelayFormat{types.RelayFormatOpenAI}, Endpoints: []dto.ProtocolEndpoint{{Format: types.RelayFormatOpenAI, Path: path}}}}
			require.Error(t, policy.Validate())
			channel := model.Channel{}
			channel.SetSetting(dto.ChannelSettings{ProtocolRouting: &policy})
			require.Error(t, channel.ValidateSettings())
		})
	}
	require.Error(t, (&dto.ProtocolRoutingSettings{Enabled: true}).Validate())
	require.NoError(t, (&dto.ProtocolRoutingSettings{}).Validate())
}

func TestProtocolEndpointCapabilitiesAfterOverrides(t *testing.T) {
	endpoint := dto.ProtocolEndpoint{Format: types.RelayFormatOpenAI, Path: "/v1/chat/completions", Verified: true}
	for _, tc := range []struct{ name, feature, request string }{
		{"stream", "stream", `{"stream":true}`},
		{"message tools", "tools", `{"messages":[{"role":"user","content":"hi","tools":[{"type":"function","function":{"name":"f"}}]}]}`},
		{"namespaced client tools", "tools", `{"tools":[{"type":"namespace","name":"client","tools":[{"type":"function","name":"run","parameters":{"type":"object","properties":{"tools":{"type":"array"}}}}]}]}`},
		{"tool result", "tools", `{"messages":[{"role":"tool","content":"result"}]}`},
		{"parallel tools", "parallel_tools", `{"parallel_tool_calls":true}`},
		{"file", "files", `{"messages":[{"role":"user","content":[{"type":"file","file":{"file_id":"f"}}]}]}`},
		{"audio output", "audio", `{"modalities":["text","audio"]}`},
		{"video input", "video", `{"input":[{"type":"input_video","video_url":"https://example.test"}]}`},
		{"schema", "structured_output", `{"text":{"format":{"type":"json_schema","schema":{"type":"object"}}}}`},
		{"reasoning", "reasoning", `{"output_config":{"effort":"high"}}`},
		{"hosted tool", "hosted_tools", `{"web_search_options":{}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var request map[string]any
			require.NoError(t, common.UnmarshalJsonStr(tc.request, &request))
			require.Error(t, ValidateProtocolEndpointFeatures(endpoint, endpoint.Format, request))
			declared := endpoint
			declared.Features = []string{tc.feature}
			require.NoError(t, ValidateProtocolEndpointFeatures(declared, declared.Format, request))
		})
	}
	for _, raw := range []string{
		`{"tool_choice":{"type":"auto","disable_parallel_tool_use":false}}`,
		`{"messages":[{"role":"assistant","tool_calls":[{"type":"function"},{"type":"function"}]}]}`,
		`{"messages":[{"role":"assistant","content":[{"type":"tool_use","name":"one"},{"type":"tool_use","name":"two"}]}]}`,
	} {
		var request map[string]any
		require.NoError(t, common.UnmarshalJsonStr(raw, &request))
		declared := endpoint
		declared.Features = []string{"tools"}
		require.Error(t, ValidateProtocolEndpointFeatures(declared, declared.Format, request))
		declared.Features = append(declared.Features, "parallel_tools")
		require.NoError(t, ValidateProtocolEndpointFeatures(declared, declared.Format, request))
	}
	all := endpoint
	all.Features = []string{"stateful", "background", "tools", "hosted_tools", "audio"}
	for _, request := range []map[string]any{{"messages": []any{map[string]any{"role": "assistant", "audio": map[string]any{"id": "audio_existing"}}}}, {"tools": []any{map[string]any{"type": "code_interpreter", "container": "cntr_existing"}}}, {"background": true}, {"conversation": "private"}, {"input": []any{map[string]any{"type": "item_reference", "id": "private"}}}} {
		require.ErrorContains(t, ValidateProtocolEndpointFeatures(all, all.Format, request), "stateful/background")
	}
	require.NoError(t, ValidateProtocolEndpointFeatures(endpoint, endpoint.Format, &dto.GeneralOpenAIRequest{Model: "m"}))
	require.Error(t, ValidateProtocolEndpointFeatures(endpoint, types.RelayFormatClaude, map[string]any{}))
	var namespacedHosted map[string]any
	require.NoError(t, common.UnmarshalJsonStr(`{"tools":[{"type":"namespace","name":"client","tools":[{"type":"web_search"}]}]}`, &namespacedHosted))
	clientTools := dto.ProtocolEndpoint{Format: types.RelayFormatOpenAIResponses, Path: "/responses", Verified: true, Features: []string{"tools"}}
	require.ErrorContains(t, ValidateProtocolEndpointFeatures(clientTools, clientTools.Format, namespacedHosted), "hosted_tools")
	clientTools.Features = append(clientTools.Features, "hosted_tools")
	require.NoError(t, ValidateProtocolEndpointFeatures(clientTools, clientTools.Format, namespacedHosted))
	var nested any = map[string]any{"type": "function", "name": "run"}
	for depth := 0; depth < 34; depth++ {
		nested = map[string]any{"type": "namespace", "name": "client", "tools": []any{nested}}
	}
	require.ErrorContains(t, ValidateProtocolEndpointFeatures(clientTools, clientTools.Format, map[string]any{"tools": []any{nested}}), "unsupported_content")
}

func TestProtocolConversionRejectsSilentLoss(t *testing.T) {
	for _, tc := range []struct {
		name           string
		source, target types.RelayFormat
		raw, field     string
	}{
		{"multiple choices", types.RelayFormatOpenAI, types.RelayFormatClaude, `{"n":2}`, "n"},
		{"chat stop", types.RelayFormatOpenAI, types.RelayFormatOpenAIResponses, `{"stop":["private"]}`, "stop"},
		{"chat logprobs", types.RelayFormatOpenAI, types.RelayFormatOpenAIResponses, `{"logprobs":true,"top_logprobs":2}`, "logprobs"},
		{"chat schema", types.RelayFormatOpenAI, types.RelayFormatClaude, `{"response_format":{"type":"json_schema","json_schema":{"name":"private"}}}`, "response_format"},
		{"responses schema", types.RelayFormatOpenAIResponses, types.RelayFormatClaude, `{"text":{"format":{"type":"json_object"}}}`, "text"},
		{"responses namespace conversion", types.RelayFormatOpenAIResponses, types.RelayFormatOpenAI, `{"tools":[{"type":"namespace","name":"private","tools":[{"type":"function","name":"run","parameters":{"type":"object"}}]}]}`, "tools"},
		{"claude stop", types.RelayFormatClaude, types.RelayFormatOpenAIResponses, `{"stop_sequences":["private"]}`, "stop_sequences"},
		{"claude schema", types.RelayFormatClaude, types.RelayFormatOpenAIResponses, `{"output_config":{"format":{"type":"json_schema"}}}`, "output_config"},
		{"claude tool choice", types.RelayFormatClaude, types.RelayFormatOpenAI, `{"tool_choice":{"type":"any"}}`, "tool_choice"},
		{"claude image tool result to chat", types.RelayFormatClaude, types.RelayFormatOpenAI, `{"messages":[{"role":"user","content":[{"type":"tool_result","tool_use_id":"call_1","content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"private"}}]}]}]}`, "messages"},
		{"claude strict tool to chat", types.RelayFormatClaude, types.RelayFormatOpenAI, `{"tools":[{"name":"lookup","strict":true,"input_schema":{"type":"object"}}]}`, "tools"},
		{"claude tool cache to chat", types.RelayFormatClaude, types.RelayFormatOpenAI, `{"tools":[{"name":"lookup","cache_control":{"type":"ephemeral"},"input_schema":{"type":"object"}}]}`, "tools"},
		{"claude tool cache to responses", types.RelayFormatClaude, types.RelayFormatOpenAIResponses, `{"tools":[{"name":"lookup","cache_control":{"type":"ephemeral"},"input_schema":{"type":"object"}}]}`, "tools"},
		{"chat unknown stream option", types.RelayFormatOpenAI, types.RelayFormatClaude, `{"stream_options":{"include_usage":true,"future_option":"private"}}`, "stream_options"},
		{"claude error result to chat", types.RelayFormatClaude, types.RelayFormatOpenAI, `{"messages":[{"role":"user","content":[{"type":"tool_result","tool_use_id":"call_1","is_error":true,"content":"private failure"}]}]}`, "messages"},
		{"claude error result to responses", types.RelayFormatClaude, types.RelayFormatOpenAIResponses, `{"messages":[{"role":"user","content":[{"type":"tool_result","tool_use_id":"call_1","is_error":true,"content":"private failure"}]}]}`, "messages"},
		{"claude thinking", types.RelayFormatClaude, types.RelayFormatOpenAI, `{"messages":[{"role":"assistant","content":[{"type":"thinking","thinking":"private","signature":"private"}]}]}`, "messages"},
		{"claude redacted", types.RelayFormatClaude, types.RelayFormatOpenAIResponses, `{"messages":[{"role":"assistant","content":[{"type":"redacted_thinking","data":"private"}]}]}`, "messages"},
		{"audio", types.RelayFormatOpenAI, types.RelayFormatClaude, `{"messages":[{"role":"user","content":[{"type":"input_audio","input_audio":{"data":"private","format":"wav"}}]}]}`, "messages"},
		{"video", types.RelayFormatOpenAI, types.RelayFormatClaude, `{"messages":[{"role":"user","content":[{"type":"video_url","video_url":{"url":"private"}}]}]}`, "messages"},
		{"claude document", types.RelayFormatClaude, types.RelayFormatOpenAI, `{"messages":[{"role":"user","content":[{"type":"document","source":{"type":"base64","data":"private"}}]}]}`, "messages"},
		{"claude image url", types.RelayFormatClaude, types.RelayFormatOpenAI, `{"messages":[{"role":"user","content":[{"type":"image","source":{"type":"url","url":"private"}}]}]}`, "messages"},
		{"unknown source field", types.RelayFormatOpenAI, types.RelayFormatClaude, `{"future_parameter":"private"}`, "unmapped_fields"},
		{"explicit false unknown", types.RelayFormatOpenAI, types.RelayFormatClaude, `{"future_parameter":false}`, "unmapped_fields"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var request map[string]any
			require.NoError(t, common.UnmarshalJsonStr(tc.raw, &request))
			for _, policy := range []string{"", "safe", "strict"} {
				err := ValidateProtocolConversion(tc.source, tc.target, request, policy)
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.field)
				assert.NotContains(t, err.Error(), "private")
			}
			require.NoError(t, ValidateProtocolConversion(tc.source, tc.target, request, "allow"))
			require.NoError(t, ValidateProtocolConversion(tc.source, tc.source, request, "safe"))
		})
	}
	for _, source := range []types.RelayFormat{types.RelayFormatOpenAI, types.RelayFormatClaude, types.RelayFormatOpenAIResponses} {
		for _, target := range []types.RelayFormat{types.RelayFormatOpenAI, types.RelayFormatClaude, types.RelayFormatOpenAIResponses} {
			require.NoError(t, ValidateProtocolConversion(source, target, map[string]any{"model": "m", "stream": true, "temperature": 0.0}, "safe"))
			require.ErrorContains(t, ValidateProtocolConversion(source, target, map[string]any{"background": true}, "allow"), "stateful/background")
		}
	}
}

func TestProtocolConversionPreservesSupportedControls(t *testing.T) {
	for _, policy := range []string{"safe", "strict"} {
		for _, target := range []types.RelayFormat{types.RelayFormatClaude, types.RelayFormatOpenAIResponses} {
			for _, includeUsage := range []bool{false, true} {
				require.NoError(t, ValidateProtocolConversion(types.RelayFormatOpenAI, target, map[string]any{"stream_options": map[string]any{"include_usage": includeUsage}}, policy))
			}
		}
		for _, tc := range []struct {
			target types.RelayFormat
			raw    string
		}{
			{types.RelayFormatOpenAI, `{"messages":[{"role":"assistant","content":[{"type":"text","text":"before"},{"type":"tool_use","id":"call_1","name":"lookup","input":{}}]}]}`},
			{types.RelayFormatOpenAI, `{"tools":[{"name":"lookup","input_schema":{"type":"object","properties":{"cache_control":{"type":"string"}}}}]}`},
			{types.RelayFormatOpenAIResponses, `{"tools":[{"name":"lookup","strict":true,"input_schema":{"type":"object"}}]}`},
		} {
			var request map[string]any
			require.NoError(t, common.UnmarshalJsonStr(tc.raw, &request))
			require.NoError(t, ValidateProtocolConversion(types.RelayFormatClaude, tc.target, request, policy))
		}
	}
}

func TestProtocolContextEditingIsNativeCapability(t *testing.T) {
	for _, source := range []types.RelayFormat{types.RelayFormatClaude, types.RelayFormatOpenAIResponses} {
		for _, context := range []any{map[string]any{}, map[string]any{"edits": []any{map[string]any{"type": "clear_thinking_20251015", "keep": "private-directive"}}}, []any{map[string]any{"type": "compaction", "compact_threshold": 1000}}} {
			request := map[string]any{"model": "m", "context_management": context}
			endpoint := dto.ProtocolEndpoint{Format: source, Path: "/v1/native", Verified: true, Features: []string{"context_editing"}}
			policy := &dto.ProtocolRoutingSettings{Enabled: true, Defaults: dto.ProtocolModelPolicy{EntryFormats: []types.RelayFormat{source}, Endpoints: []dto.ProtocolEndpoint{endpoint}}}
			require.NoError(t, policy.Validate())
			candidates, err := BuildProtocolCandidates(policy, "m", source, request)
			require.NoError(t, err)
			require.Len(t, candidates, 1)
			require.NoError(t, ValidateProtocolEndpointFeatures(endpoint, source, request))
			require.NoError(t, ValidateProtocolConversion(source, source, request, "safe"))
			undeclared := endpoint
			undeclared.Features = nil
			err = ValidateProtocolEndpointFeatures(undeclared, source, request)
			require.Error(t, err)
			assert.NotContains(t, err.Error(), "stateful/background")
			for _, target := range []types.RelayFormat{types.RelayFormatOpenAI, types.RelayFormatClaude, types.RelayFormatOpenAIResponses} {
				if source == target {
					continue
				}
				for _, loss := range []string{"", "safe", "strict"} {
					err = ValidateProtocolConversion(source, target, request, loss)
					require.ErrorContains(t, err, "context_management")
					assert.NotContains(t, err.Error(), "stateful/background")
					assert.NotContains(t, err.Error(), "private-directive")
				}
				require.NoError(t, ValidateProtocolConversion(source, target, request, "allow"))
			}
		}
	}
	raw, err := common.Marshal(map[string]any{"edits": []any{map[string]any{"type": "clear_thinking_20251015"}}})
	require.NoError(t, err)
	typed := &dto.ClaudeRequest{Model: "m", ContextManagement: raw}
	endpoint := dto.ProtocolEndpoint{Format: types.RelayFormatClaude, Path: "/v1/messages", Verified: true, Features: []string{"context_editing"}}
	require.NoError(t, ValidateProtocolEndpointFeatures(endpoint, endpoint.Format, typed))
	require.NoError(t, ValidateProtocolConversion(types.RelayFormatClaude, types.RelayFormatClaude, typed, "strict"))
	// A declared capability cannot make the Chat DTO retain an unknown field.
	chat := dto.ProtocolEndpoint{Format: types.RelayFormatOpenAI, Path: "/v1/chat/completions", Verified: true, Features: []string{"context_editing"}}
	request := map[string]any{"model": "m", "context_management": map[string]any{}}
	require.ErrorContains(t, ValidateProtocolEndpointFeatures(chat, chat.Format, request), "context_management")
	for _, source := range []types.RelayFormat{types.RelayFormatOpenAI, types.RelayFormatClaude, types.RelayFormatOpenAIResponses} {
		policy := &dto.ProtocolRoutingSettings{Enabled: true, Defaults: dto.ProtocolModelPolicy{EntryFormats: []types.RelayFormat{source}, Endpoints: []dto.ProtocolEndpoint{chat}}}
		candidates, err := BuildProtocolCandidates(policy, "m", source, request)
		require.Error(t, err)
		assert.Empty(t, candidates)
	}
	require.NoError(t, ValidateProtocolEndpointFeatures(chat, chat.Format, map[string]any{"model": "m"}))

}

func TestProtocolStatefulErrorsNameFieldsWithoutValues(t *testing.T) {
	for _, field := range []string{"previous_response_id", "conversation", "container", "background"} {
		request := map[string]any{field: "private-value", "context_management": map[string]any{}}
		endpoint := dto.ProtocolEndpoint{Format: types.RelayFormatOpenAIResponses, Path: "/v1/responses", Verified: true, Features: []string{"context_editing", "stateful", "background"}}
		for _, err := range []error{ValidateProtocolEndpointFeatures(endpoint, endpoint.Format, request), ValidateProtocolConversion(endpoint.Format, types.RelayFormatClaude, request, "allow")} {
			require.ErrorContains(t, err, "stateful/background")
			assert.Contains(t, err.Error(), field)
			assert.NotContains(t, err.Error(), "private-value")
			assert.NotContains(t, err.Error(), "context_management")
		}
	}
}
