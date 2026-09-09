package service

import (
	"fmt"
	"sort"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/protocol_setting"
)

type ProtocolCandidate struct {
	Endpoint   dto.ProtocolEndpoint
	LossPolicy string
}

// BuildProtocolCandidates plans only declared, verified capabilities. It never
// probes providers or includes request content in diagnostics. Nil/disabled
// configuration returns no plan so legacy routing can continue unchanged.
func BuildProtocolCandidates(settings *dto.ProtocolRoutingSettings, upstreamModel string, source types.RelayFormat, request any) ([]ProtocolCandidate, error) {
	if settings == nil || !settings.Enabled {
		return nil, nil
	}
	if err := settings.Validate(); err != nil {
		return nil, err
	}
	policy, err := protocol_setting.Get().Resolve(settings, upstreamModel)
	if err != nil {
		return nil, err
	}
	allowed := false
	for _, format := range policy.EntryFormats {
		if format == source {
			allowed = true
			break
		}
	}
	if !allowed {
		return nil, fmt.Errorf("protocol routing does not allow this entry format")
	}
	required, err := protocolRequiredFeatures(request)
	if err != nil {
		return nil, err
	}
	loss := policy.LossPolicy
	if loss == "" {
		loss = "safe"
	}
	candidates := make([]ProtocolCandidate, 0, len(policy.Endpoints))
	rejections := make([]string, 0, len(policy.Endpoints))
	for _, endpoint := range policy.Endpoints {
		if !endpoint.Verified {
			continue
		}
		if err := validateProtocolFeatures(endpoint, required); err != nil {
			// Only validated format identifiers and canonical capability names are
			// included: never expose request values or administrator endpoint paths.
			rejections = append(rejections, fmt.Sprintf("%s: %s", endpoint.Format, err))
			continue
		}
		candidates = append(candidates, ProtocolCandidate{Endpoint: endpoint, LossPolicy: loss})
	}
	if len(candidates) == 0 {
		if len(rejections) == 0 {
			rejections = append(rejections, "no endpoints are verified")
		}
		return nil, fmt.Errorf("no verified protocol endpoint supports the request capabilities; %s", strings.Join(rejections, "; "))
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		return candidates[i].Endpoint.Format == source && candidates[j].Endpoint.Format != source
	})
	return candidates, nil
}

// ValidateProtocolEndpointFeatures rechecks the final wire request after channel
// parameter overrides and conversion; callers must invoke it before sending.
func ValidateProtocolEndpointFeatures(endpoint dto.ProtocolEndpoint, format types.RelayFormat, request any) error {
	if !endpoint.Verified || endpoint.Format != format {
		return fmt.Errorf("protocol endpoint is unverified or has a different wire format")
	}
	required, err := protocolRequiredFeatures(request)
	if err != nil {
		return err
	}
	return validateProtocolFeatures(endpoint, required)
}

func validateProtocolFeatures(endpoint dto.ProtocolEndpoint, required map[string]bool) error {
	// The Chat DTO has no context_management field. A declaration cannot make
	// this wire adapter preserve context-editing directives.
	if endpoint.Format == types.RelayFormatOpenAI && required["context_editing"] {
		return fmt.Errorf("OpenAI Chat endpoint cannot preserve context_management")
	}
	// Responses Lite additional_tools is an input extension that the native
	// Responses adapters currently reject. It is supported only by the explicit
	// Responses-to-Chat bridge, regardless of what another endpoint declares.
	if endpoint.Format != types.RelayFormatOpenAI && required["responses_lite_bridge"] {
		return fmt.Errorf("responses_lite_bridge requires an OpenAI Chat endpoint")
	}
	available := make(map[string]bool, len(endpoint.Features))
	for _, feature := range endpoint.Features {
		available[feature] = true
	}
	missing := make([]string, 0, len(required))
	for feature := range required {
		if !available[feature] {
			missing = append(missing, feature)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return fmt.Errorf("missing capabilities: %s", strings.Join(missing, ", "))
	}
	return nil
}

func protocolRequiredFeatures(request any) (map[string]bool, error) {
	body, ok := request.(map[string]any)
	if !ok {
		data, err := common.Marshal(request)
		if err != nil {
			return nil, fmt.Errorf("protocol routing cannot inspect request")
		}
		if err = common.Unmarshal(data, &body); err != nil {
			return nil, fmt.Errorf("protocol routing cannot inspect request")
		}
	}
	if body == nil {
		return nil, fmt.Errorf("protocol routing requires a request object")
	}
	required, err := protocolRequestFeatures(body)
	if err != nil {
		return nil, err
	}
	if err := protocolStatefulRoutingError(body, required); err != nil {
		return nil, err
	}
	return required, nil
}

// Context-editing directives are capabilities, not stored conversation
// references. Only actual stateful/background controls reach this diagnostic.
func protocolStatefulRoutingError(body map[string]any, required map[string]bool) error {
	if !required["stateful"] && !required["background"] {
		return nil
	}
	fields := make([]string, 0, 4)
	for _, field := range []string{"background", "container", "conversation", "previous_response_id"} {
		if protocolValuePresent(body[field]) {
			fields = append(fields, field)
		}
	}
	if len(fields) == 0 {
		return fmt.Errorf("stateful/background routing is not supported")
	}
	return fmt.Errorf("stateful/background routing is not supported for fields: %s", strings.Join(fields, ", "))
}

func protocolRequestFeatures(body map[string]any) (map[string]bool, error) {
	required := map[string]bool{}
	for key, feature := range map[string]string{
		"stream": "stream", "parallel_tool_calls": "parallel_tools", "tools": "tools", "functions": "tools", "function_call": "tools", "tool_choice": "tools",
		"reasoning": "reasoning", "reasoning_effort": "reasoning", "thinking": "reasoning", "enable_thinking": "reasoning", "thinking_budget": "reasoning", "think": "reasoning", "thinking_token_budget": "reasoning",
		"audio": "audio", "web_search_options": "hosted_tools", "search_parameters": "hosted_tools", "mcp_servers": "hosted_tools", "enable_search": "hosted_tools", "web_search": "hosted_tools",
		"previous_response_id": "stateful", "conversation": "stateful", "context_management": "context_editing", "container": "stateful", "background": "background",
	} {
		value := body[key]
		if protocolValuePresent(value) {
			required[feature] = true
		}
	}
	if choice, ok := body["tool_choice"].(map[string]any); ok {
		if disabled, exists := choice["disable_parallel_tool_use"].(bool); exists && !disabled {
			required["parallel_tools"] = true
		}
	}
	if config, ok := body["output_config"].(map[string]any); ok {
		if protocolValuePresent(config["effort"]) {
			required["reasoning"] = true
		}
		if protocolValuePresent(config["format"]) {
			required["structured_output"] = true
		}
	}
	for _, key := range []string{"response_format", "output_format"} {
		if config, ok := body[key].(map[string]any); ok && config["type"] != "text" {
			required["structured_output"] = true
		}
	}
	if config, ok := body["text"].(map[string]any); ok {
		if format, ok := config["format"].(map[string]any); ok && format["type"] != "text" {
			required["structured_output"] = true
		}
	}
	if modalities, ok := body["modalities"].([]any); ok {
		for _, modality := range modalities {
			if modality == "audio" {
				required["audio"] = true
			}
		}
	}
	inspectProtocolTools(body["tools"], required, 0)
	shellExecution := "unknown"
	declaredTools, _ := body["tools"].([]any)
	for _, raw := range declaredTools {
		spec, _ := raw.(map[string]any)
		if spec["type"] != "shell" {
			continue
		}
		classified := ClassifyProtocolTool(spec)
		if shellExecution != "unknown" && shellExecution != classified {
			shellExecution = "unknown"
			break
		}
		shellExecution = classified
	}
	for _, key := range []string{"messages", "input", "system"} {
		if err := inspectProtocolContent(body[key], required, 0, shellExecution, key); err != nil {
			return nil, err
		}
	}
	return required, nil
}

// inspectProtocolTools reads protocol fields only; arbitrary JSON schemas and
// tool arguments may contain the same names and are deliberately left opaque.
func inspectProtocolTools(value any, required map[string]bool, depth int) {
	if depth > 32 {
		required["unsupported_content"] = true
		return
	}
	tools, ok := value.([]any)
	if !ok {
		return
	}
	if len(tools) > 0 {
		required["tools"] = true
	}
	for _, tool := range tools {
		spec, ok := tool.(map[string]any)
		if !ok {
			required["unsupported_content"] = true
			continue
		}
		kind, _ := spec["type"].(string)
		if kind == "namespace" {
			// A namespace groups client functions/custom tools; it does not
			// require provider-hosted execution. Inspect its declarations only,
			// keeping schemas, arguments and descriptions opaque.
			inspectProtocolTools(spec["tools"], required, depth+1)
		}
		switch ClassifyProtocolTool(spec) {
		case "server":
			required["hosted_tools"] = true
		case "unknown":
			required["unknown_tool_execution"] = true
		}
		if environment, ok := spec["environment"].(map[string]any); ok && environment["type"] == "container_reference" {
			required["stateful"] = true
		}
		if container, exists := spec["container"]; exists && protocolValuePresent(container) {
			fresh, ok := container.(map[string]any)
			if !ok || fresh["type"] != "auto" || protocolValuePresent(fresh["id"]) {
				required["stateful"] = true
			}
		}
	}
}

func protocolValuePresent(value any) bool {
	switch v := value.(type) {
	case nil:
		return false
	case bool:
		return v
	case string:
		return v != "" && v != "none"
	case []any:
		return len(v) > 0
	case map[string]any:
		return true
	default:
		return true
	}
}

// Inspect only protocol content containers, never tool schemas, metadata, or
// strings containing user JSON. Unknown content fails closed.
func inspectProtocolContent(value any, required map[string]bool, depth int, shellExecution, location string) error {
	if depth > 32 {
		return fmt.Errorf("unsupported_content: protocol nesting exceeds the limit")
	}
	switch v := value.(type) {
	case []any:
		for i, item := range v {
			if err := inspectProtocolContent(item, required, depth+1, shellExecution, fmt.Sprintf("%s[%d]", location, i)); err != nil {
				return err
			}
		}
	case map[string]any:
		kind, _ := v["type"].(string)
		switch kind {
		case "", "message", "text", "input_text", "output_text", "refusal":
		case "image", "image_url", "input_image":
			required["images"] = true
		case "document", "file", "input_file":
			required["files"] = true
		case "audio", "input_audio", "output_audio":
			required["audio"] = true
		case "video", "video_url", "input_video":
			required["video"] = true
		case "tool_use", "tool_result", "function_call", "function_call_output", "custom_tool_call", "custom_tool_call_output", "computer_call", "computer_call_output", "local_shell_call", "local_shell_call_output", "apply_patch_call", "apply_patch_call_output":
			required["tools"] = true
		case "additional_tools":
			required["tools"] = true
			required["responses_lite_bridge"] = true
		case "tool_search_call", "tool_search_output":
			required["tools"] = true
			if v["execution"] != "client" {
				required["hosted_tools"] = true
			}
		case "shell_call", "shell_call_output":
			required["tools"] = true
			if shellExecution == "server" {
				required["hosted_tools"] = true
			} else if shellExecution != "client" {
				required["unknown_tool_execution"] = true
			}
		case "thinking", "redacted_thinking", "reasoning":
			required["reasoning"] = true
		case "item_reference":
			required["stateful"] = true
		default:
			if strings.HasPrefix(kind, "server_tool") || strings.HasPrefix(kind, "web_search") || strings.HasPrefix(kind, "computer") || strings.HasPrefix(kind, "mcp_") {
				required["hosted_tools"] = true
			} else {
				// Expose only known protocol vocabulary, never arbitrary client
				// values. The location contains only canonical keys and indices.
				diagnostic := "unrecognized"
				switch kind {
				case "compaction", "compaction_summary", "context_compaction", "compaction_trigger", "agent_message", "encrypted_content", "reasoning_text", "summary_text":
					diagnostic = kind
				}
				return fmt.Errorf("unsupported_content at %s (type=%s)", location, diagnostic)
			}
		}
		if v["role"] == "tool" || v["role"] == "function" || protocolValuePresent(v["tool_calls"]) || protocolValuePresent(v["function_call"]) {
			required["tools"] = true
		}
		inspectProtocolTools(v["tools"], required, depth+1)
		if audio, ok := v["audio"].(map[string]any); ok {
			required["audio"] = true
			if protocolValuePresent(audio["id"]) {
				required["stateful"] = true
			}
		}
		if calls, ok := v["tool_calls"].([]any); ok && len(calls) > 1 {
			required["parallel_tools"] = true
		}
		if content, ok := v["content"].([]any); ok {
			calls := 0
			for _, block := range content {
				if item, ok := block.(map[string]any); ok && item["type"] == "tool_use" {
					calls++
				}
			}
			if calls > 1 {
				required["parallel_tools"] = true
			}
		}
		if protocolValuePresent(v["reasoning_content"]) || protocolValuePresent(v["reasoning"]) {
			required["reasoning"] = true
		}
		for _, key := range []string{"content", "output"} {
			// Function output objects are application JSON, unlike arrays of
			// typed protocol content. Do not interpret their keys as controls.
			if key == "output" && (kind == "function_call_output" || kind == "custom_tool_call_output") {
				if _, object := v[key].(map[string]any); object {
					continue
				}
			}
			if err := inspectProtocolContent(v[key], required, depth+1, shellExecution, location+"."+key); err != nil {
				return err
			}
		}
	}
	return nil
}

// ValidateProtocolConversion checks the fidelity of the currently implemented
// request converters independently of provider capability declarations. Native
// requests keep their protocol semantics. An explicit allow policy accepts loss,
// but cannot enable provider-hosted conversation state or background execution.
func ValidateProtocolConversion(source, target types.RelayFormat, request any, lossPolicy string, targetFeatures ...string) error {
	if !dto.IsProtocolRoutingFormat(source) || !dto.IsProtocolRoutingFormat(target) {
		return fmt.Errorf("unsupported protocol conversion format")
	}
	switch lossPolicy {
	case "", "safe", "strict", "allow":
	default:
		return fmt.Errorf("invalid protocol conversion loss policy")
	}
	data, err := common.Marshal(request)
	if err != nil {
		return fmt.Errorf("protocol conversion cannot inspect request")
	}
	var body map[string]any
	if common.Unmarshal(data, &body) != nil || body == nil {
		return fmt.Errorf("protocol conversion requires a request object")
	}
	required, err := protocolRequestFeatures(body)
	if err != nil {
		return err
	}
	if err := protocolStatefulRoutingError(body, required); err != nil {
		return err
	}
	if required["responses_lite_bridge"] {
		enabled := false
		for _, feature := range targetFeatures {
			if feature == "responses_lite_bridge" {
				enabled = true
				break
			}
		}
		if target != types.RelayFormatOpenAI || !enabled {
			return fmt.Errorf("responses_lite_bridge requires an enabled OpenAI Chat endpoint")
		}
	}
	if source == target || lossPolicy == "allow" {
		return nil
	}

	allowed := "model stream temperature top_p tools"
	switch source {
	case types.RelayFormatOpenAI:
		allowed += " messages max_tokens max_completion_tokens reasoning_effort reasoning stream_options"
		if target == types.RelayFormatClaude {
			allowed += " top_k stop tool_choice parallel_tool_calls"
		} else {
			allowed += " tool_choice parallel_tool_calls response_format frequency_penalty presence_penalty user store metadata prompt_cache_key enable_thinking thinking_budget"
		}
	case types.RelayFormatOpenAIResponses:
		allowed += " input instructions max_output_tokens tool_choice parallel_tool_calls reasoning"
		if target == types.RelayFormatOpenAI {
			allowed += " stream_options top_logprobs text frequency_penalty presence_penalty user store metadata safety_identifier prompt_cache_key prompt_cache_retention enable_thinking thinking_budget service_tier"
		}
	case types.RelayFormatClaude:
		allowed += " messages system max_tokens thinking output_config"
		if target == types.RelayFormatOpenAI {
			allowed += " top_k stop_sequences"
		} else {
			allowed += " max_tokens_to_sample tool_choice metadata service_tier"
		}
	}
	allowedFields := map[string]bool{}
	for _, field := range strings.Fields(allowed) {
		allowedFields[field] = true
	}
	rejected := map[string]bool{}
	for field, value := range body {
		if !protocolConversionValuePresent(value) {
			continue
		}
		if field == "n" && value == float64(1) {
			continue
		}
		if allowedFields[field] {
			continue
		}
		// Diagnostics use canonical field names only, never arbitrary client keys.
		switch field {
		case "context_management", "n", "stop", "logprobs", "top_logprobs", "response_format", "text", "stop_sequences", "output_format", "tool_choice", "stream_options", "top_k", "metadata", "service_tier", "user", "store", "max_tokens_to_sample", "frequency_penalty", "presence_penalty", "seed", "logit_bias", "audio", "modalities", "prediction", "verbosity", "functions", "function_call", "web_search_options", "search_parameters", "include", "max_tool_calls", "truncation", "prompt_cache_retention", "cache_control", "extra_body":
			rejected[field] = true
		default:
			rejected["unmapped_fields"] = true
		}
	}
	// The bridge preserves include_usage in downstream framing while requesting
	// upstream accounting separately. Other stream options have no such mapping.
	if source == types.RelayFormatOpenAI && protocolConversionValuePresent(body["stream_options"]) {
		options, ok := body["stream_options"].(map[string]any)
		if !ok {
			rejected["stream_options"] = true
		}
		for field, value := range options {
			if _, valid := value.(bool); field != "include_usage" || !valid {
				rejected["stream_options"] = true
			}
		}
	}
	if config, ok := body["output_config"].(map[string]any); ok {
		for field, value := range config {
			if field != "effort" && protocolConversionValuePresent(value) {
				rejected["output_config"] = true
			}
		}
	}
	// Hosted/custom tools need protocol-specific execution and result semantics;
	// function schema compatibility alone cannot establish that equivalence.
	if required["hosted_tools"] {
		rejected["tools"] = true
	}
	if tools, ok := body["tools"].([]any); ok {
		for _, value := range tools {
			tool, ok := value.(map[string]any)
			if !ok {
				rejected["tools"] = true
				continue
			}
			if protocolConversionValuePresent(tool["cache_control"]) || (source == types.RelayFormatClaude && target == types.RelayFormatOpenAI && tool["strict"] == true) {
				rejected["tools"] = true
			}
			kind, _ := tool["type"].(string)
			if kind != "" && kind != "function" {
				rejected["tools"] = true
			}
			if target == types.RelayFormatClaude {
				if function, ok := tool["function"].(map[string]any); ok && protocolConversionValuePresent(function["strict"]) {
					rejected["tools"] = true
				}
				if protocolConversionValuePresent(tool["strict"]) {
					rejected["tools"] = true
				}
			}
		}
	}
	for _, field := range []string{"messages", "input", "system"} {
		if protocolContentConversionLoss(body[field], source, target, 0, required["responses_lite_bridge"]) {
			rejected[field] = true
		}
	}
	if len(rejected) == 0 {
		return nil
	}
	fields := make([]string, 0, len(rejected))
	for field := range rejected {
		fields = append(fields, field)
	}
	sort.Strings(fields)
	return fmt.Errorf("protocol conversion cannot preserve fields: %s", strings.Join(fields, ", "))
}

// Explicit false and zero are meaningful controls and cannot be discarded just
// because they are Go zero values. Only absent/null/empty values are ignorable.
func protocolConversionValuePresent(value any) bool {
	switch v := value.(type) {
	case nil:
		return false
	case string:
		return v != ""
	case []any:
		return len(v) > 0
	default:
		return true
	}
}

func protocolContentConversionLoss(value any, source, target types.RelayFormat, depth int, responsesLiteBridge bool) bool {
	if depth > 32 {
		return true
	}
	switch v := value.(type) {
	case []any:
		for _, item := range v {
			if protocolContentConversionLoss(item, source, target, depth+1, responsesLiteBridge) {
				return true
			}
		}
	case map[string]any:
		kind, _ := v["type"].(string)
		if responsesLiteBridge && kind == "additional_tools" {
			return false
		}
		for _, field := range []string{"signature", "encrypted_content", "reasoning_content", "reasoning", "cache_control", "citations", "annotations", "tools", "audio", "name"} {
			if protocolConversionValuePresent(v[field]) {
				// Function/tool call names are protocol data retained by these converters.
				if field == "name" && (kind == "tool_use" || kind == "function_call" || responsesLiteBridge && (kind == "custom_tool_call" || kind == "custom_tool_call_output")) {
					continue
				}
				return true
			}
		}
		if source == types.RelayFormatClaude && kind == "tool_result" {
			if v["is_error"] == true {
				return true
			}
			if target == types.RelayFormatOpenAI {
				if blocks, ok := v["content"].([]any); ok {
					for _, value := range blocks {
						block, ok := value.(map[string]any)
						if !ok || (block["type"] != "text" && block["type"] != "input_text") {
							return true
						}
					}
				}
			}
		}
		switch kind {
		case "thinking", "redacted_thinking", "reasoning", "document", "file", "input_file", "audio", "input_audio", "output_audio", "video", "video_url", "input_video":
			return true
		case "image":
			if source == types.RelayFormatClaude && target == types.RelayFormatOpenAI {
				imageSource, ok := v["source"].(map[string]any)
				if !ok || imageSource["type"] != "base64" {
					return true
				}
			}
		}
		if target == types.RelayFormatClaude {
			if imageURL, ok := v["image_url"].(map[string]any); ok && protocolConversionValuePresent(imageURL["detail"]) && imageURL["detail"] != "auto" {
				return true
			}
		}
		// Content/output contain protocol blocks; arguments/input schemas and textual
		// tool output are opaque user data and must never be recursively inspected.
		for _, field := range []string{"content", "output"} {
			if field == "output" && (kind == "function_call_output" || kind == "custom_tool_call_output") {
				if _, object := v[field].(map[string]any); object {
					continue
				}
			}
			if protocolContentConversionLoss(v[field], source, target, depth+1, responsesLiteBridge) {
				return true
			}
		}
	}
	return false
}
