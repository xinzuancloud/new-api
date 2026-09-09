package oairesponses

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/relayconvert/convmeta"
	kitutil "github.com/QuantumNous/new-api/relaykit/relayconvert/kitutil"
	"github.com/QuantumNous/new-api/relaykit/relayconvert/reasoning"
)

const responsesLiteAdditionalToolsType = "additional_tools"

const (
	responsesInputTypeFunctionCall       = "function_call"
	responsesInputTypeFunctionCallOutput = "function_call_output"
	responsesInputTypeCustomToolCall     = "custom_tool_call"
	responsesInputTypeCustomToolOutput   = "custom_tool_call_output"
)

const (
	ResponsesInputTypeFunctionCall       = responsesInputTypeFunctionCall
	ResponsesInputTypeFunctionCallOutput = responsesInputTypeFunctionCallOutput
	ResponsesInputTypeCustomToolCall     = responsesInputTypeCustomToolCall
	ResponsesInputTypeCustomToolOutput   = responsesInputTypeCustomToolOutput
)

func ResponsesRequestToChatCompletionsRequest(req *dto.OpenAIResponsesRequest) (*dto.GeneralOpenAIRequest, error) {
	if req == nil {
		return nil, errors.New("request is nil")
	}
	if req.Model == "" {
		return nil, errors.New("model is required")
	}
	if err := validateResponsesRequestChatUnsupportedFields(req); err != nil {
		return nil, err
	}

	messages, err := responsesRequestMessagesToChat(req)
	if err != nil {
		return nil, err
	}

	tools, err := responsesRequestToolsToChat(req.Tools)
	if err != nil {
		return nil, err
	}

	toolChoice, err := responsesRequestToolChoiceToChat(req.ToolChoice)
	if err != nil {
		return nil, err
	}

	responseFormat, err := responsesRequestTextToChatResponseFormat(req.Text)
	if err != nil {
		return nil, err
	}

	out := &dto.GeneralOpenAIRequest{
		Model:                req.Model,
		Messages:             messages,
		Stream:               req.Stream,
		StreamOptions:        req.StreamOptions,
		MaxCompletionTokens:  req.MaxOutputTokens,
		Temperature:          req.Temperature,
		TopP:                 req.TopP,
		TopLogProbs:          req.TopLogProbs,
		ResponseFormat:       responseFormat,
		Tools:                tools,
		ToolChoice:           toolChoice,
		User:                 req.User,
		Store:                req.Store,
		Metadata:             req.Metadata,
		SafetyIdentifier:     req.SafetyIdentifier,
		PromptCacheRetention: req.PromptCacheRetention,
		EnableThinking:       req.EnableThinking,
		ThinkingBudget:       req.ThinkingBudget,
	}

	out.FrequencyPenalty, err = responsesRawFloat(req.FrequencyPenalty)
	if err != nil {
		return nil, fmt.Errorf("invalid frequency_penalty: %w", err)
	}
	out.PresencePenalty, err = responsesRawFloat(req.PresencePenalty)
	if err != nil {
		return nil, fmt.Errorf("invalid presence_penalty: %w", err)
	}

	if reasoningIntent, err := reasoning.FromOpenAIResponses(req); err != nil {
		return nil, reasoning.AsClientError(err)
	} else if err := reasoning.ApplyToOpenAIChat(out, reasoningIntent); err != nil {
		return nil, reasoning.AsClientError(err)
	}
	if req.ServiceTier != "" {
		out.ServiceTier, _ = kitutil.Marshal(req.ServiceTier)
	}
	if len(req.ParallelToolCalls) > 0 && kitutil.GetJsonType(req.ParallelToolCalls) == "boolean" {
		var parallelToolCalls bool
		if err := kitutil.Unmarshal(req.ParallelToolCalls, &parallelToolCalls); err == nil {
			out.ParallelTooCalls = &parallelToolCalls
		}
	}
	if len(req.PromptCacheKey) > 0 && kitutil.GetJsonType(req.PromptCacheKey) == "string" {
		var promptCacheKey string
		if err := kitutil.Unmarshal(req.PromptCacheKey, &promptCacheKey); err == nil {
			out.PromptCacheKey = promptCacheKey
		}
	}

	return out, nil
}

func prepareResponsesLiteBridgeRequest(req *dto.OpenAIResponsesRequest) (*convmeta.ResponsesLiteBridge, error) {
	if req == nil || !rawJSONPresent(req.Input) || kitutil.GetJsonType(req.Input) != "array" {
		return nil, nil
	}
	var input []map[string]any
	if err := kitutil.Unmarshal(req.Input, &input); err != nil {
		return nil, fmt.Errorf("invalid Responses Lite input: %w", err)
	}
	additionalIndex := -1
	for index, item := range input {
		if strings.TrimSpace(kitutil.Interface2String(item["type"])) != responsesLiteAdditionalToolsType {
			continue
		}
		if additionalIndex >= 0 {
			return nil, errors.New("Responses Lite input contains multiple additional_tools items")
		}
		additionalIndex = index
	}
	if additionalIndex < 0 {
		return nil, nil
	}

	additionalTools, ok := input[additionalIndex]["tools"].([]any)
	if !ok || len(additionalTools) == 0 {
		return nil, errors.New("Responses Lite additional_tools must contain tools")
	}
	var existingTools []any
	if rawJSONPresent(req.Tools) {
		if kitutil.GetJsonType(req.Tools) != "array" || kitutil.Unmarshal(req.Tools, &existingTools) != nil {
			return nil, errors.New("Responses Lite top-level tools must be an array")
		}
	}
	usedNames := make(map[string]bool, len(existingTools)+len(additionalTools))
	for _, rawTool := range existingTools {
		if tool, ok := rawTool.(map[string]any); ok {
			usedNames[strings.TrimSpace(kitutil.Interface2String(tool["name"]))] = true
		}
	}

	convertedTools := append([]any(nil), existingTools...)
	mappings := make([]convmeta.ResponsesLiteTool, 0, len(additionalTools))
	toolNumber := 0
	addTool := func(rawTool any, namespace string) error {
		tool, ok := rawTool.(map[string]any)
		if !ok {
			return errors.New("Responses Lite tool declaration must be an object")
		}
		kind := strings.TrimSpace(kitutil.Interface2String(tool["type"]))
		name := strings.TrimSpace(kitutil.Interface2String(tool["name"]))
		if name == "" || (kind != "function" && kind != "custom") {
			return fmt.Errorf("Responses Lite tool type %q cannot be bridged", kind)
		}
		aliasBase := responsesLiteSafeToolName(name)
		alias := fmt.Sprintf("bridge_%d_%s", toolNumber, aliasBase)
		for usedNames[alias] {
			toolNumber++
			alias = fmt.Sprintf("bridge_%d_%s", toolNumber, aliasBase)
		}
		usedNames[alias] = true
		toolNumber++

		converted := map[string]any{
			"type": "function",
			"name": alias,
		}
		if description := kitutil.Interface2String(tool["description"]); description != "" {
			converted["description"] = description
		}
		if kind == "function" {
			parameters, ok := tool["parameters"].(map[string]any)
			if !ok {
				return fmt.Errorf("Responses Lite function %q is missing parameters", name)
			}
			converted["parameters"] = parameters
			if strict, ok := tool["strict"].(bool); ok {
				converted["strict"] = strict
			}
		} else {
			format, ok := tool["format"].(map[string]any)
			if !ok || strings.TrimSpace(kitutil.Interface2String(format["type"])) != "grammar" || strings.TrimSpace(kitutil.Interface2String(format["syntax"])) == "" || strings.TrimSpace(kitutil.Interface2String(format["definition"])) == "" {
				return fmt.Errorf("Responses Lite custom tool %q requires a grammar format", name)
			}
			converted["parameters"] = map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"properties": map[string]any{
					"input": map[string]any{"type": "string", "minLength": 1},
				},
				"required": []any{"input"},
			}
			description := strings.TrimSpace(kitutil.Interface2String(converted["description"]))
			instruction := "Return the complete raw custom-tool input in the input string field."
			if description == "" {
				converted["description"] = instruction
			} else {
				converted["description"] = description + "\n\n" + instruction
			}
		}
		convertedTools = append(convertedTools, converted)
		mappings = append(mappings, convmeta.ResponsesLiteTool{Alias: alias, Kind: kind, Namespace: namespace, Name: name})
		return nil
	}

	for _, rawTool := range additionalTools {
		tool, ok := rawTool.(map[string]any)
		if !ok {
			return nil, errors.New("Responses Lite tool declaration must be an object")
		}
		if strings.TrimSpace(kitutil.Interface2String(tool["type"])) != "namespace" {
			if err := addTool(rawTool, ""); err != nil {
				return nil, err
			}
			continue
		}
		namespace := strings.TrimSpace(kitutil.Interface2String(tool["name"]))
		children, ok := tool["tools"].([]any)
		if namespace == "" || !ok || len(children) == 0 {
			return nil, errors.New("Responses Lite namespace must contain named tools")
		}
		for _, child := range children {
			if nested, ok := child.(map[string]any); ok && strings.TrimSpace(kitutil.Interface2String(nested["type"])) == "namespace" {
				return nil, errors.New("nested Responses Lite namespaces cannot be bridged")
			}
			if err := addTool(child, namespace); err != nil {
				return nil, err
			}
		}
	}
	bridge, err := convmeta.NewResponsesLiteBridge(mappings)
	if err != nil {
		return nil, err
	}

	bridgedInput := make([]map[string]any, 0, len(input)-1)
	for index, item := range input {
		if index == additionalIndex {
			continue
		}
		kind := strings.TrimSpace(kitutil.Interface2String(item["type"]))
		switch kind {
		case responsesInputTypeCustomToolCall:
			namespace := strings.TrimSpace(kitutil.Interface2String(item["namespace"]))
			name := strings.TrimSpace(kitutil.Interface2String(item["name"]))
			alias, ok := bridge.AliasFor("custom", namespace, name)
			if !ok {
				return nil, fmt.Errorf("Responses Lite custom tool history references an undeclared tool")
			}
			customInput, ok := item["input"].(string)
			if !ok || customInput == "" {
				return nil, fmt.Errorf("Responses Lite custom tool history has invalid input")
			}
			arguments, err := kitutil.Marshal(map[string]any{"input": customInput})
			if err != nil {
				return nil, err
			}
			item["type"] = responsesInputTypeFunctionCall
			item["name"] = alias
			item["arguments"] = string(arguments)
			delete(item, "namespace")
			delete(item, "input")
		case responsesInputTypeCustomToolOutput:
			item["type"] = responsesInputTypeFunctionCallOutput
			delete(item, "namespace")
			delete(item, "name")
		case responsesInputTypeFunctionCall:
			namespace := strings.TrimSpace(kitutil.Interface2String(item["namespace"]))
			if namespace != "" {
				name := strings.TrimSpace(kitutil.Interface2String(item["name"]))
				alias, ok := bridge.AliasFor("function", namespace, name)
				if !ok {
					return nil, fmt.Errorf("Responses Lite function history references an undeclared tool")
				}
				item["name"] = alias
				delete(item, "namespace")
			}
		case responsesInputTypeFunctionCallOutput:
			delete(item, "namespace")
			delete(item, "name")
		case "message":
			// Chat-compatible providers commonly implement the older role set.
			// A Responses developer instruction has the same precedence purpose as
			// a Chat system instruction, so preserve it through that role.
			if strings.TrimSpace(kitutil.Interface2String(item["role"])) == "developer" {
				item["role"] = "system"
			}
		}
		bridgedInput = append(bridgedInput, item)
	}
	req.Input, err = kitutil.Marshal(bridgedInput)
	if err != nil {
		return nil, err
	}
	req.Tools, err = kitutil.Marshal(convertedTools)
	if err != nil {
		return nil, err
	}
	return bridge, nil
}

func PrepareResponsesLiteBridgeRequest(req *dto.OpenAIResponsesRequest) (*convmeta.ResponsesLiteBridge, error) {
	return prepareResponsesLiteBridgeRequest(req)
}

func responsesLiteSafeToolName(name string) string {
	var builder strings.Builder
	for _, char := range name {
		if char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || char == '_' || char == '-' {
			builder.WriteRune(char)
		} else {
			builder.WriteByte('_')
		}
		if builder.Len() >= 48 {
			break
		}
	}
	if builder.Len() == 0 {
		return "tool"
	}
	return builder.String()
}

func validateResponsesRequestChatUnsupportedFields(req *dto.OpenAIResponsesRequest) error {
	unsupported := make([]string, 0, 4)
	if rawJSONPresent(req.Conversation) {
		unsupported = append(unsupported, "conversation")
	}
	if strings.TrimSpace(req.PreviousResponseID) != "" {
		unsupported = append(unsupported, "previous_response_id")
	}
	if rawJSONPresent(req.Prompt) {
		unsupported = append(unsupported, "prompt")
	}
	if rawJSONPresent(req.ContextManagement) {
		unsupported = append(unsupported, "context_management")
	}
	if len(unsupported) > 0 {
		return fmt.Errorf("responses to chat conversion does not support stateful fields: %s", strings.Join(unsupported, ", "))
	}
	return nil
}

func ValidateRequestChatUnsupportedFields(req *dto.OpenAIResponsesRequest) error {
	return validateResponsesRequestChatUnsupportedFields(req)
}

func responsesRequestMessagesToChat(req *dto.OpenAIResponsesRequest) ([]dto.Message, error) {
	messages := make([]dto.Message, 0)
	if rawJSONPresent(req.Instructions) {
		instructions, err := responsesJSONString(req.Instructions)
		if err != nil {
			return nil, fmt.Errorf("invalid instructions: %w", err)
		}
		if strings.TrimSpace(instructions) != "" {
			messages = append(messages, dto.Message{Role: "system", Content: instructions})
		}
	}

	if !rawJSONPresent(req.Input) {
		return messages, nil
	}

	switch kitutil.GetJsonType(req.Input) {
	case "string":
		input, err := responsesJSONString(req.Input)
		if err != nil {
			return nil, fmt.Errorf("invalid input string: %w", err)
		}
		messages = append(messages, dto.Message{Role: "user", Content: input})
		return messages, nil
	case "array":
		var items []map[string]any
		if err := kitutil.Unmarshal(req.Input, &items); err != nil {
			return nil, fmt.Errorf("invalid input array: %w", err)
		}
		for _, item := range items {
			nextMessages, err := responsesInputItemToChatMessages(item, messages)
			if err != nil {
				return nil, err
			}
			messages = nextMessages
		}
		return messages, nil
	default:
		return nil, fmt.Errorf("unsupported responses input type %q", kitutil.GetJsonType(req.Input))
	}
}

func responsesInputItemToChatMessages(item map[string]any, messages []dto.Message) ([]dto.Message, error) {
	itemType := strings.TrimSpace(kitutil.Interface2String(item["type"]))
	switch itemType {
	case responsesInputTypeFunctionCall:
		toolCall, err := responsesFunctionCallItemToChatToolCall(item)
		if err != nil {
			return nil, err
		}
		return appendToolCallToLastAssistant(messages, toolCall), nil
	case responsesInputTypeCustomToolCall:
		toolCall, err := responsesCustomToolCallItemToChatToolCall(item)
		if err != nil {
			return nil, err
		}
		return appendToolCallToLastAssistant(messages, toolCall), nil
	case responsesInputTypeFunctionCallOutput:
		callID := strings.TrimSpace(kitutil.Interface2String(item["call_id"]))
		content := responseToolOutputToChatContent(item["output"])
		return append(messages, dto.Message{Role: "tool", ToolCallId: callID, Content: content}), nil
	}

	role := strings.TrimSpace(kitutil.Interface2String(item["role"]))
	if role == "" {
		role = "user"
	}
	content, err := responsesInputContentToChatContent(item["content"])
	if err != nil {
		return nil, err
	}
	return append(messages, dto.Message{Role: role, Content: content}), nil
}

func responsesInputContentToChatContent(content any) (any, error) {
	if content == nil {
		return "", nil
	}

	switch value := content.(type) {
	case string:
		return value, nil
	case []any:
		return responsesContentPartsToChatContent(value)
	case []map[string]any:
		parts := make([]any, 0, len(value))
		for _, part := range value {
			parts = append(parts, part)
		}
		return responsesContentPartsToChatContent(parts)
	default:
		return content, nil
	}
}

func responsesContentPartsToChatContent(parts []any) (any, error) {
	chatParts := make([]any, 0, len(parts))
	var textOnly strings.Builder
	onlyText := true

	for _, rawPart := range parts {
		part, ok := rawPart.(map[string]any)
		if !ok {
			onlyText = false
			chatParts = append(chatParts, rawPart)
			continue
		}

		partType := strings.TrimSpace(kitutil.Interface2String(part["type"]))
		switch partType {
		case "input_text", "output_text", "text":
			text := kitutil.Interface2String(part["text"])
			textOnly.WriteString(text)
			chatParts = append(chatParts, map[string]any{
				"type": dto.ContentTypeText,
				"text": text,
			})
		case "input_image":
			onlyText = false
			chatParts = append(chatParts, map[string]any{
				"type":      dto.ContentTypeImageURL,
				"image_url": responsesImagePartToChatImageURL(part),
			})
		case "input_file":
			onlyText = false
			chatParts = append(chatParts, map[string]any{
				"type": dto.ContentTypeFile,
				"file": responsesFilePartToChatFile(part),
			})
		case "input_audio":
			onlyText = false
			chatParts = append(chatParts, map[string]any{
				"type":        dto.ContentTypeInputAudio,
				"input_audio": responsesPartPayload(part, "input_audio"),
			})
		case "input_video":
			onlyText = false
			chatParts = append(chatParts, map[string]any{
				"type":      dto.ContentTypeVideoUrl,
				"video_url": responsesVideoPartToChatVideoURL(part),
			})
		default:
			onlyText = false
			chatParts = append(chatParts, part)
		}
	}

	if onlyText {
		return textOnly.String(), nil
	}
	return chatParts, nil
}

func responsesFunctionCallItemToChatToolCall(item map[string]any) (dto.ToolCallRequest, error) {
	name := strings.TrimSpace(kitutil.Interface2String(item["name"]))
	if name == "" {
		return dto.ToolCallRequest{}, errors.New("function_call item is missing name")
	}
	return dto.ToolCallRequest{
		ID:   responsesCallID(item),
		Type: "function",
		Function: dto.FunctionRequest{
			Name:      name,
			Arguments: responsesArgumentsString(item["arguments"]),
		},
	}, nil
}

func responsesCustomToolCallItemToChatToolCall(item map[string]any) (dto.ToolCallRequest, error) {
	raw, err := kitutil.Marshal(item)
	if err != nil {
		return dto.ToolCallRequest{}, err
	}
	return dto.ToolCallRequest{
		ID:     responsesCallID(item),
		Type:   dto.CustomType,
		Custom: raw,
		Function: dto.FunctionRequest{
			Name:      strings.TrimSpace(kitutil.Interface2String(item["name"])),
			Arguments: responsesArgumentsString(item["input"]),
		},
	}, nil
}

func appendToolCallToLastAssistant(messages []dto.Message, toolCall dto.ToolCallRequest) []dto.Message {
	if len(messages) == 0 || messages[len(messages)-1].Role != "assistant" {
		messages = append(messages, dto.Message{Role: "assistant"})
	}

	idx := len(messages) - 1
	toolCalls := messages[idx].ParseToolCalls()
	toolCalls = append(toolCalls, toolCall)
	toolCallsRaw, _ := kitutil.Marshal(toolCalls)
	messages[idx].ToolCalls = toolCallsRaw
	return messages
}

func responsesRequestToolsToChat(raw json.RawMessage) ([]dto.ToolCallRequest, error) {
	if !rawJSONPresent(raw) {
		return nil, nil
	}

	var tools []map[string]any
	if err := kitutil.Unmarshal(raw, &tools); err != nil {
		return nil, fmt.Errorf("invalid tools: %w", err)
	}

	out := make([]dto.ToolCallRequest, 0, len(tools))
	for _, tool := range tools {
		toolType := strings.TrimSpace(kitutil.Interface2String(tool["type"]))
		if toolType == "function" {
			out = append(out, dto.ToolCallRequest{
				Type: "function",
				Function: dto.FunctionRequest{
					Name:        strings.TrimSpace(kitutil.Interface2String(tool["name"])),
					Description: kitutil.Interface2String(tool["description"]),
					Parameters:  tool["parameters"],
				},
			})
			continue
		}

		rawTool, err := kitutil.Marshal(tool)
		if err != nil {
			return nil, err
		}
		out = append(out, dto.ToolCallRequest{
			Type:   toolType,
			Custom: rawTool,
		})
	}
	return out, nil
}

func responsesRequestToolChoiceToChat(raw json.RawMessage) (any, error) {
	if !rawJSONPresent(raw) {
		return nil, nil
	}
	if kitutil.GetJsonType(raw) == "string" {
		var choice string
		if err := kitutil.Unmarshal(raw, &choice); err != nil {
			return nil, fmt.Errorf("invalid tool_choice: %w", err)
		}
		return choice, nil
	}

	var choice map[string]any
	if err := kitutil.Unmarshal(raw, &choice); err != nil {
		return nil, fmt.Errorf("invalid tool_choice: %w", err)
	}
	if kitutil.Interface2String(choice["type"]) == "function" {
		name := strings.TrimSpace(kitutil.Interface2String(choice["name"]))
		if name != "" {
			return map[string]any{
				"type": "function",
				"function": map[string]any{
					"name": name,
				},
			}, nil
		}
	}
	return choice, nil
}

func RequestToolChoiceToChat(raw json.RawMessage) (any, error) {
	return responsesRequestToolChoiceToChat(raw)
}

func responsesRequestTextToChatResponseFormat(raw json.RawMessage) (*dto.ResponseFormat, error) {
	if !rawJSONPresent(raw) {
		return nil, nil
	}

	var textConfig map[string]any
	if err := kitutil.Unmarshal(raw, &textConfig); err != nil {
		return nil, fmt.Errorf("invalid text config: %w", err)
	}
	format, ok := textConfig["format"].(map[string]any)
	if !ok {
		return nil, nil
	}

	formatType := strings.TrimSpace(kitutil.Interface2String(format["type"]))
	if formatType == "" {
		return nil, nil
	}

	out := &dto.ResponseFormat{Type: formatType}
	if formatType == "json_schema" {
		schemaRaw, err := kitutil.Marshal(format)
		if err != nil {
			return nil, err
		}
		out.JsonSchema = schemaRaw
	}
	return out, nil
}

func RequestTextToChatResponseFormat(raw json.RawMessage) (*dto.ResponseFormat, error) {
	return responsesRequestTextToChatResponseFormat(raw)
}

func responsesImagePartToChatImageURL(part map[string]any) any {
	if imageURL, ok := part["image_url"]; ok {
		return imageURL
	}
	imageURL := map[string]any{}
	for _, key := range []string{"url", "file_id", "detail"} {
		if value, ok := part[key]; ok {
			imageURL[key] = value
		}
	}
	if len(imageURL) == 0 {
		return part
	}
	return imageURL
}

func responsesFilePartToChatFile(part map[string]any) any {
	if file, ok := part["file"]; ok {
		return file
	}
	file := map[string]any{}
	for _, key := range []string{"file_id", "file_data", "filename", "file_url"} {
		if value, ok := part[key]; ok {
			file[key] = value
		}
	}
	if len(file) == 0 {
		return part
	}
	return file
}

func responsesVideoPartToChatVideoURL(part map[string]any) any {
	if videoURL, ok := part["video_url"]; ok {
		if videoURLMap, ok := videoURL.(map[string]any); ok {
			if url := kitutil.Interface2String(videoURLMap["url"]); url != "" {
				return url
			}
		}
		return videoURL
	}
	if url := kitutil.Interface2String(part["url"]); url != "" {
		return url
	}
	return responsesPartPayload(part, "video_url")
}

func responsesPartPayload(part map[string]any, key string) any {
	if value, ok := part[key]; ok {
		return value
	}
	payload := make(map[string]any, len(part))
	for k, value := range part {
		if k == "type" {
			continue
		}
		payload[k] = value
	}
	return payload
}

func responsesCallID(item map[string]any) string {
	callID := strings.TrimSpace(kitutil.Interface2String(item["call_id"]))
	if callID != "" {
		return callID
	}
	return strings.TrimSpace(kitutil.Interface2String(item["id"]))
}

func CallID(item map[string]any) string {
	return responsesCallID(item)
}

func responsesArgumentsString(value any) string {
	switch v := value.(type) {
	case nil:
		return ""
	case string:
		return v
	default:
		raw, err := kitutil.Marshal(v)
		if err != nil {
			return kitutil.Interface2String(v)
		}
		return string(raw)
	}
}

func responseToolOutputToChatContent(value any) any {
	switch v := value.(type) {
	case nil:
		return ""
	case string:
		return v
	default:
		raw, err := kitutil.Marshal(v)
		if err != nil {
			return fmt.Sprintf("%v", v)
		}
		return string(raw)
	}
}

func responsesRawFloat(raw json.RawMessage) (*float64, error) {
	if !rawJSONPresent(raw) {
		return nil, nil
	}
	var value float64
	if err := kitutil.Unmarshal(raw, &value); err != nil {
		return nil, err
	}
	return &value, nil
}

func responsesJSONString(raw json.RawMessage) (string, error) {
	if kitutil.GetJsonType(raw) != "string" {
		return string(raw), nil
	}
	var value string
	if err := kitutil.Unmarshal(raw, &value); err != nil {
		return "", err
	}
	return value, nil
}

func rawJSONPresent(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	return kitutil.GetJsonType(raw) != "null"
}

func JSONString(raw json.RawMessage) (string, error) {
	return responsesJSONString(raw)
}

func RawJSONPresent(raw json.RawMessage) bool {
	return rawJSONPresent(raw)
}
