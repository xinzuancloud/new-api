package service

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/types"
)

const ProtocolProbeResponseLimit = 1024 * 1024

// Fixed fixtures cannot contain an administrator-supplied URL, prompt or code.
// Returned client tool calls are inspected, never executed.
func BuildProtocolProbeFixture(format types.RelayFormat, modelName, check string, maxTokens int) ([]byte, bool, error) {
	if maxTokens < 1 || maxTokens > 1024 {
		return nil, false, fmt.Errorf("max_output_tokens must be between 1 and 1024")
	}
	if !typesProbeFormat(format) {
		return nil, false, fmt.Errorf("unsupported probe format")
	}
	stream := check == "stream" || check == "tools" || check == "namespaces" || check == "web_search"
	prompt := "Reply with the word OK."
	switch check {
	case "text", "stream":
	case "tools", "namespaces":
		prompt = "Call newapi_probe exactly once with value 7. Do not answer with text."
	case "images":
		prompt = "What solid color fills this image? Reply with the color name only."
	case "web_search":
		prompt = "Use web search once to find the official Go programming language website and cite the result briefly."
	default:
		return nil, false, fmt.Errorf("unknown probe check")
	}
	if check == "namespaces" && format != types.RelayFormatOpenAIResponses {
		return nil, false, fmt.Errorf("namespaces require Responses")
	}
	body := map[string]any{"model": modelName, "stream": stream}
	content := any(prompt)
	if check == "images" {
		img := image.NewRGBA(image.Rect(0, 0, 32, 32))
		for y := 0; y < 32; y++ {
			for x := 0; x < 32; x++ {
				img.Set(x, y, color.RGBA{R: 255, A: 255})
			}
		}
		var buffer bytes.Buffer
		if err := png.Encode(&buffer, img); err != nil {
			return nil, false, err
		}
		data := base64.StdEncoding.EncodeToString(buffer.Bytes())
		switch format {
		case types.RelayFormatClaude:
			content = []any{map[string]any{"type": "image", "source": map[string]any{"type": "base64", "media_type": "image/png", "data": data}}, map[string]any{"type": "text", "text": prompt}}
		case types.RelayFormatOpenAI:
			content = []any{map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:image/png;base64," + data}}, map[string]any{"type": "text", "text": prompt}}
		case types.RelayFormatOpenAIResponses:
			content = []any{map[string]any{"type": "input_image", "image_url": "data:image/png;base64," + data}, map[string]any{"type": "input_text", "text": prompt}}
		}
	}
	if format == types.RelayFormatOpenAIResponses {
		body["input"] = []any{map[string]any{"role": "user", "content": content}}
		body["max_output_tokens"] = maxTokens
		body["store"] = false
	} else {
		body["messages"] = []any{map[string]any{"role": "user", "content": content}}
		body["max_tokens"] = maxTokens
	}
	schema := map[string]any{"type": "object", "properties": map[string]any{"value": map[string]any{"type": "integer", "enum": []int{7}}}, "required": []string{"value"}, "additionalProperties": false}
	if check == "tools" || check == "namespaces" {
		fn := map[string]any{"name": "newapi_probe", "description": "A diagnostic function. Do not execute.", "parameters": schema}
		switch format {
		case types.RelayFormatClaude:
			body["tools"] = []any{map[string]any{"name": "newapi_probe", "description": "A diagnostic function. Do not execute.", "input_schema": schema}}
			body["tool_choice"] = map[string]any{"type": "tool", "name": "newapi_probe"}
		case types.RelayFormatOpenAI:
			body["tools"] = []any{map[string]any{"type": "function", "function": fn}}
			body["tool_choice"] = map[string]any{"type": "function", "function": map[string]any{"name": "newapi_probe"}}
		case types.RelayFormatOpenAIResponses:
			fn["type"] = "function"
			if check == "namespaces" {
				body["tools"] = []any{map[string]any{"type": "namespace", "name": "probe_namespace", "description": "Diagnostic tools", "tools": []any{fn}}}
				body["tool_choice"] = "required"
			} else {
				body["tools"] = []any{fn}
				body["tool_choice"] = map[string]any{"type": "function", "name": "newapi_probe"}
			}
		}
	}
	if check == "web_search" {
		switch format {
		case types.RelayFormatClaude:
			body["tools"] = []any{map[string]any{"type": "web_search_20250305", "name": "web_search", "max_uses": 1}}
		case types.RelayFormatOpenAIResponses:
			body["tools"] = []any{map[string]any{"type": "web_search", "search_context_size": "low"}}
			body["include"] = []string{"web_search_call.action.sources"}
			body["max_tool_calls"] = 1
		case types.RelayFormatOpenAI:
			body["web_search_options"] = map[string]any{"search_context_size": "low"}
		}
	}
	data, err := common.Marshal(body)
	return data, stream, err
}

func typesProbeFormat(format types.RelayFormat) bool {
	return format == types.RelayFormatOpenAI || format == types.RelayFormatClaude || format == types.RelayFormatOpenAIResponses
}

type protocolProbeCall struct {
	name, namespace, arguments string
	input                      map[string]any
}
type protocolProbeEvidence struct {
	text                                               strings.Builder
	calls                                              map[string]*protocolProbeCall
	terminal, finish, failed, searchCall, searchResult bool
	inputTokens, outputTokens                          int64
}

func probeObject(value any) map[string]any { v, _ := value.(map[string]any); return v }
func probeString(value any) string         { v, _ := value.(string); return v }
func probeArray(value any) []any           { v, _ := value.([]any); return v }
func probeIndex(value any) string          { return fmt.Sprint(value) }
func (e *protocolProbeEvidence) usage(value any) {
	u := probeObject(value)
	for _, key := range []string{"input_tokens", "prompt_tokens"} {
		if n, ok := u[key].(float64); ok && n >= 0 && n <= 1e12 {
			e.inputTokens = max(e.inputTokens, int64(n))
		}
	}
	for _, key := range []string{"output_tokens", "completion_tokens"} {
		if n, ok := u[key].(float64); ok && n >= 0 && n <= 1e12 {
			e.outputTokens = max(e.outputTokens, int64(n))
		}
	}
}
func (e *protocolProbeEvidence) claudeBlock(block map[string]any, index string) {
	switch probeString(block["type"]) {
	case "text":
		e.text.WriteString(probeString(block["text"]))
	case "tool_use":
		e.calls[index] = &protocolProbeCall{name: probeString(block["name"]), input: probeObject(block["input"])}
	case "server_tool_use":
		if block["name"] == "web_search" {
			e.searchCall = true
		}
	case "web_search_tool_result":
		for _, raw := range probeArray(block["content"]) {
			item := probeObject(raw)
			if item["type"] == "web_search_result" && probeString(item["url"]) != "" {
				e.searchResult = true
			}
		}
	}
}
func (e *protocolProbeEvidence) responsesItem(item map[string]any) {
	switch item["type"] {
	case "message":
		for _, raw := range probeArray(item["content"]) {
			block := probeObject(raw)
			if block["type"] == "output_text" {
				e.text.WriteString(probeString(block["text"]))
			}
		}
	case "function_call":
		e.calls[probeString(item["id"])+probeString(item["call_id"])] = &protocolProbeCall{name: probeString(item["name"]), namespace: probeString(item["namespace"]), arguments: probeString(item["arguments"])}
	case "web_search_call":
		if item["status"] == "completed" {
			e.searchCall = true
			action := probeObject(item["action"])
			if len(probeArray(action["sources"])) > 0 || len(probeArray(item["results"])) > 0 {
				e.searchResult = true
			}
		}
	}
}
func (e *protocolProbeEvidence) consume(format types.RelayFormat, event map[string]any, stream bool) {
	if event["error"] != nil || event["type"] == "error" || event["type"] == "response.failed" {
		e.failed = true
	}
	e.usage(event["usage"])
	switch format {
	case types.RelayFormatClaude:
		switch event["type"] {
		case "message":
			for i, raw := range probeArray(event["content"]) {
				e.claudeBlock(probeObject(raw), strconv.Itoa(i))
			}
			reason := probeString(event["stop_reason"])
			e.finish = reason == "end_turn" || reason == "tool_use" || reason == "stop_sequence"
			e.terminal = e.finish && !stream
		case "message_start":
			e.usage(probeObject(event["message"])["usage"])
		case "content_block_start":
			e.claudeBlock(probeObject(event["content_block"]), probeIndex(event["index"]))
		case "content_block_delta":
			delta := probeObject(event["delta"])
			if delta["type"] == "text_delta" {
				e.text.WriteString(probeString(delta["text"]))
			}
			if call := e.calls[probeIndex(event["index"])]; call != nil {
				call.arguments += probeString(delta["partial_json"])
			}
		case "message_delta":
			reason := probeString(probeObject(event["delta"])["stop_reason"])
			e.finish = reason == "end_turn" || reason == "tool_use" || reason == "stop_sequence"
		case "message_stop":
			e.terminal = true
		}
	case types.RelayFormatOpenAI:
		for _, raw := range probeArray(event["choices"]) {
			choice := probeObject(raw)
			reason := probeString(choice["finish_reason"])
			if reason == "stop" || reason == "tool_calls" || reason == "function_call" {
				e.finish = true
			}
			delta := probeObject(choice["delta"])
			if !stream {
				delta = probeObject(choice["message"])
				e.terminal = e.finish
			}
			e.text.WriteString(probeString(delta["content"]))
			for i, tool := range probeArray(delta["tool_calls"]) {
				item := probeObject(tool)
				index := probeIndex(choice["index"]) + ":" + probeIndex(item["index"])
				if !stream {
					index = strconv.Itoa(i)
				}
				call := e.calls[index]
				if call == nil {
					call = &protocolProbeCall{}
					e.calls[index] = call
				}
				fn := probeObject(item["function"])
				call.name += probeString(fn["name"])
				call.arguments += probeString(fn["arguments"])
			}
			// Chat search surfaces citations rather than a server-call item. Require
			// a canonical URL citation and a successful terminal response.
			for _, a := range probeArray(delta["annotations"]) {
				annotation := probeObject(a)
				if annotation["type"] == "url_citation" && probeString(probeObject(annotation["url_citation"])["url"]) != "" {
					e.searchCall = true
					e.searchResult = true
				}
			}
		}
	case types.RelayFormatOpenAIResponses:
		response := event
		if stream {
			switch event["type"] {
			case "response.completed":
				response = probeObject(event["response"])
			case "response.output_item.done":
				e.responsesItem(probeObject(event["item"]))
				return
			case "response.incomplete":
				e.failed = true
				return
			default:
				return
			}
		}
		e.usage(response["usage"])
		if response["status"] == "completed" {
			e.terminal = true
			e.finish = true
		}
		for _, raw := range probeArray(response["output"]) {
			e.responsesItem(probeObject(raw))
		}
	}
}

// EvaluateProtocolProbe accepts only protocol evidence, never HTTP 200 alone.
// A rejection applies to this fixture; it does not revoke declared capability.
func EvaluateProtocolProbe(format types.RelayFormat, check string, status int, body []byte) model.ProtocolProbeResult {
	result := model.ProtocolProbeResult{Outcome: "unknown", Reason: "insufficient_evidence", HTTPStatus: status}
	if status < 200 || status >= 300 {
		result.Reason = "upstream_status"
		if status >= 400 && status < 500 && status != 408 && status != 429 {
			result.Outcome = "rejected"
			result.Reason = "fixture_rejected"
		}
		return result
	}
	if len(body) > ProtocolProbeResponseLimit {
		result.Reason = "response_too_large"
		return result
	}
	stream := check == "stream" || check == "tools" || check == "namespaces" || check == "web_search"
	evidence := protocolProbeEvidence{calls: map[string]*protocolProbeCall{}}
	if stream {
		scanner := bufio.NewScanner(bytes.NewReader(body))
		scanner.Buffer(make([]byte, 4096), ProtocolProbeResponseLimit)
		var data strings.Builder
		// SSE data may span multiple lines. Only dispatch complete frames.
		for scanner.Scan() {
			line := strings.TrimSuffix(scanner.Text(), "\r")
			if line != "" {
				if strings.HasPrefix(line, "data:") {
					if data.Len() > 0 {
						data.WriteByte('\n')
					}
					data.WriteString(strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
				}
				continue
			}
			raw := data.String()
			data.Reset()
			if raw == "" {
				continue
			}
			if raw == "[DONE]" {
				if format == types.RelayFormatOpenAI {
					evidence.terminal = true
				}
				continue
			}
			var event map[string]any
			if common.UnmarshalJsonStr(raw, &event) != nil {
				evidence.failed = true
				continue
			}
			evidence.consume(format, event, true)
		}
		if scanner.Err() != nil || data.Len() > 0 {
			evidence.failed = true
		}
	} else {
		var response map[string]any
		if common.Unmarshal(body, &response) != nil {
			result.Reason = "invalid_response"
			return result
		}
		evidence.consume(format, response, false)
	}
	result.Terminal = evidence.terminal && evidence.finish && !evidence.failed
	result.InputTokens = evidence.inputTokens
	result.OutputTokens = evidence.outputTokens
	if !result.Terminal {
		result.Reason = "incomplete_response"
		return result
	}
	passed := false
	switch check {
	case "text", "stream":
		passed = strings.TrimSpace(evidence.text.String()) != ""
	case "images":
		text := strings.Trim(strings.ToLower(strings.TrimSpace(evidence.text.String())), ".!")
		passed = text == "red" || text == "红色" || text == "紅色"
	case "tools", "namespaces":
		for _, call := range evidence.calls {
			if call.name != "newapi_probe" || (check == "namespaces" && call.namespace != "probe_namespace") {
				continue
			}
			input := call.input
			if call.arguments != "" {
				if common.UnmarshalJsonStr(call.arguments, &input) != nil {
					continue
				}
			}
			if len(input) == 1 && input["value"] == float64(7) {
				passed = true
			}
		}
	case "web_search":
		passed = evidence.searchCall && evidence.searchResult
	}
	if !passed {
		return result
	}
	result.Outcome = "passed"
	result.Reason = "verified_fixture"
	result.Features = []string{}
	if stream {
		result.Features = append(result.Features, "stream")
	}
	switch check {
	case "tools", "namespaces":
		result.Features = append(result.Features, "tools")
	case "images":
		result.Features = append(result.Features, "images")
	case "web_search":
		result.Features = append(result.Features, "hosted_tools")
		if format != types.RelayFormatOpenAI {
			result.Features = append(result.Features, "tools")
		}
	}
	return result
}
