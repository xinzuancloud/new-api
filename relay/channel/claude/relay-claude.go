package claude

import (
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/relayconvert"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/model_setting"

	"github.com/gin-gonic/gin"
)

func stopReasonClaude2OpenAI(reason string) string {
	return relayconvert.StopReasonClaudeToOpenAI(reason)
}

func maybeMarkClaudeRefusal(c *gin.Context, stopReason string) {
	if c == nil {
		return
	}
	if strings.EqualFold(stopReason, "refusal") {
		common.SetContextKey(c, constant.ContextKeyAdminRejectReason, "claude_stop_reason=refusal")
	}
}

func StreamResponseClaude2OpenAI(claudeResponse *dto.ClaudeResponse) *dto.ChatCompletionsStreamResponse {
	return relayconvert.StreamResponseClaude2OpenAI(claudeResponse)
}

func ResponseClaude2OpenAI(claudeResponse *dto.ClaudeResponse) *dto.OpenAITextResponse {
	return relayconvert.ResponseClaude2OpenAI(claudeResponse)
}

type ClaudeResponseInfo = relayconvert.ClaudeResponseInfo

func cacheCreationTokensForOpenAIUsage(usage *dto.Usage) int {
	if usage == nil {
		return 0
	}
	openAIUsage := relayconvert.UsageFromClaudeUsage(usage)
	if openAIUsage == nil {
		return 0
	}
	return openAIUsage.PromptTokens - usage.PromptTokens - usage.PromptTokensDetails.CachedTokens
}

func buildOpenAIStyleUsageFromClaudeUsage(usage *dto.Usage) dto.Usage {
	mapped := relayconvert.UsageFromClaudeUsage(usage)
	if mapped == nil {
		return dto.Usage{}
	}
	return *mapped
}

func buildMessageDeltaPatchUsage(claudeResponse *dto.ClaudeResponse, claudeInfo *ClaudeResponseInfo) *dto.ClaudeUsage {
	return relayconvert.BuildMessageDeltaPatchUsage(claudeResponse, claudeInfo)
}

func shouldSkipClaudeMessageDeltaUsagePatch(info *relaycommon.RelayInfo) bool {
	if model_setting.GetGlobalSettings().PassThroughRequestEnabled {
		return true
	}
	if info == nil {
		return false
	}
	return info.ChannelSetting.PassThroughBodyEnabled
}

func patchClaudeMessageDeltaUsageData(data string, usage *dto.ClaudeUsage) string {
	return relayconvert.PatchClaudeMessageDeltaUsageData(data, usage)
}

func FormatClaudeResponseInfo(claudeResponse *dto.ClaudeResponse, oaiResponse *dto.ChatCompletionsStreamResponse, claudeInfo *ClaudeResponseInfo) bool {
	return relayconvert.FormatClaudeResponseInfo(claudeResponse, oaiResponse, claudeInfo)
}

func HandleStreamResponseData(c *gin.Context, info *relaycommon.RelayInfo, claudeInfo *ClaudeResponseInfo, data string) *types.NewAPIError {
	var claudeResponse dto.ClaudeResponse
	err := common.UnmarshalJsonStr(data, &claudeResponse)
	if err != nil {
		common.SysLog("error unmarshalling stream response: " + err.Error())
		return types.NewError(err, types.ErrorCodeBadResponseBody)
	}
	if claudeError := claudeResponse.GetClaudeError(); claudeError != nil && claudeError.Type != "" {
		return types.WithClaudeError(*claudeError, http.StatusInternalServerError)
	}
	if claudeResponse.StopReason != "" {
		maybeMarkClaudeRefusal(c, claudeResponse.StopReason)
	}
	if claudeResponse.Delta != nil && claudeResponse.Delta.StopReason != nil {
		maybeMarkClaudeRefusal(c, *claudeResponse.Delta.StopReason)
	}
	observeClaudeWebSearchUsage(c, &claudeResponse)
	if info.RelayFormat == types.RelayFormatClaude {
		FormatClaudeResponseInfo(&claudeResponse, nil, claudeInfo)

		if claudeResponse.Type == "message_start" {
			// message_start, 获取usage
			if claudeResponse.Message != nil {
				info.UpstreamModelName = claudeResponse.Message.Model
			}
		} else if claudeResponse.Type == "message_delta" {
			// 确保 message_delta 的 usage 包含完整的 input_tokens 和 cache 相关字段
			// 解决 AWS Bedrock 等上游返回的 message_delta 缺少这些字段的问题
			if !shouldSkipClaudeMessageDeltaUsagePatch(info) {
				data = patchClaudeMessageDeltaUsageData(data, buildMessageDeltaPatchUsage(&claudeResponse, claudeInfo))
			}
		}
		countClaudeStreamBillableTools(c, info, &claudeResponse)
		if err := helper.ClaudeChunkData(c, claudeResponse, data); err != nil {
			return types.NewError(err, types.ErrorCodeBadResponseBody)
		}
	} else if info.RelayFormat == types.RelayFormatOpenAI {
		state, err := claudeToChatStreamState(info)
		if err != nil {
			return types.NewError(err, types.ErrorCodeBadResponseBody)
		}
		response, err := state.ConvertChunk(&claudeResponse)
		if err != nil {
			return types.NewError(err, types.ErrorCodeBadResponseBody)
		}

		if !FormatClaudeResponseInfo(&claudeResponse, response, claudeInfo) {
			return nil
		}

		countClaudeStreamBillableTools(c, info, &claudeResponse)

		if response == nil {
			return nil
		}
		return sendClaudeConvertedStreamData(c, response)
	} else if info.RelayFormat == types.RelayFormatGemini {
		state, err := claudeToGeminiStreamState(info)
		if err != nil {
			return types.NewError(err, types.ErrorCodeBadResponseBody)
		}
		results, err := service.ConvertStreamResponseChunk(c, info, state, &claudeResponse)
		if err != nil {
			return types.NewError(err, types.ErrorCodeBadResponseBody)
		}
		if !FormatClaudeResponseInfo(&claudeResponse, nil, claudeInfo) {
			return nil
		}
		countClaudeStreamBillableTools(c, info, &claudeResponse)
		if sendErr := sendGeminiStreamResults(c, results); sendErr != nil {
			return sendErr
		}
	}
	return nil
}

func claudeToChatStreamState(info *relaycommon.RelayInfo) (*relayconvert.ClaudeToChatStreamState, error) {
	if info != nil && info.ClaudeToChatStreamState != nil {
		state, ok := info.ClaudeToChatStreamState.(*relayconvert.ClaudeToChatStreamState)
		if !ok || state == nil {
			return nil, fmt.Errorf("invalid Claude-to-Chat stream state %T", info.ClaudeToChatStreamState)
		}
		return state, nil
	}

	state := relayconvert.NewClaudeToChatStreamState()
	if info != nil {
		info.ClaudeToChatStreamState = state
	}
	return state, nil
}

func claudeToGeminiStreamState(info *relaycommon.RelayInfo) (*relayconvert.ResponseStreamState, error) {
	if info != nil && info.ChatToGeminiStreamState != nil {
		state, ok := info.ChatToGeminiStreamState.(*relayconvert.ResponseStreamState)
		if !ok || state == nil {
			return nil, fmt.Errorf("invalid Claude-to-Gemini stream state %T", info.ChatToGeminiStreamState)
		}
		return state, nil
	}

	state, err := relayconvert.NewResponseStreamState(types.RelayFormatClaude, types.RelayFormatGemini, relayconvert.ResponseStreamOptions{})
	if err != nil {
		return nil, err
	}
	if info != nil {
		info.ChatToGeminiStreamState = state
	}
	return state, nil
}

func sendClaudeConvertedStreamData(c *gin.Context, response any) *types.NewAPIError {
	data, err := common.Marshal(response)
	if err != nil {
		return types.NewError(err, types.ErrorCodeBadResponseBody)
	}
	if err := c.Request.Context().Err(); err != nil {
		return types.NewError(err, types.ErrorCodeBadResponseBody)
	}
	if _, err := fmt.Fprintf(c.Writer, "data: %s\n\n", data); err != nil {
		return types.NewError(err, types.ErrorCodeBadResponseBody)
	}
	if err := helper.FlushWriter(c); err != nil {
		return types.NewError(err, types.ErrorCodeBadResponseBody)
	}
	return nil
}

func sendGeminiStreamResults(c *gin.Context, results []relayconvert.ResponseResult) *types.NewAPIError {
	for _, result := range results {
		geminiResponse, ok := result.Value.(*dto.GeminiChatResponse)
		if !ok {
			return types.NewError(fmt.Errorf("expected Gemini stream response, got %T", result.Value), types.ErrorCodeBadResponseBody)
		}
		if geminiResponse == nil {
			continue
		}
		if err := sendClaudeConvertedStreamData(c, geminiResponse); err != nil {
			return err
		}
	}
	return nil
}

func countClaudeStreamBillableTools(c *gin.Context, info *relaycommon.RelayInfo, claudeResponse *dto.ClaudeResponse) {
	if claudeResponse == nil {
		return
	}
	if claudeResponse.Type == "content_block_start" &&
		claudeResponse.ContentBlock != nil &&
		claudeResponse.ContentBlock.Type == "tool_use" {
		info.CountBillableToolCall(dto.BuildInCallToolUse, claudeResponse.ContentBlock.Name)
	}
}

type claudeWebSearchResult struct {
	id       string
	billable bool
}

type claudeWebSearchBillingState struct {
	pending   map[int]claudeWebSearchResult
	completed map[string]struct{}
	reported  *int
	observed  bool
}

func billableClaudeWebSearchResult(block *dto.ClaudeMediaMessage) bool {
	if strings.TrimSpace(block.ToolUseId) == "" || strings.TrimSpace(block.ErrorCode) != "" || (block.IsError != nil && *block.IsError) {
		return false
	}
	results, ok := block.Content.([]any)
	if !ok { // Error results are objects; successful results are arrays, including [].
		return false
	}
	for _, result := range results {
		item, ok := result.(map[string]any)
		if !ok || item["type"] != "web_search_result" {
			return false
		}
	}
	return true
}

// Completed search results provide a fallback only when final server statistics
// are absent. One result block is one invocation, regardless of its hit count.
func observeClaudeWebSearchUsage(c *gin.Context, response *dto.ClaudeResponse) {
	value, _ := c.Get("claude_web_search_billing_state")
	state, _ := value.(*claudeWebSearchBillingState)
	if state == nil {
		state = &claudeWebSearchBillingState{pending: map[int]claudeWebSearchResult{}, completed: map[string]struct{}{}}
		c.Set("claude_web_search_billing_state", state)
	}
	switch response.Type {
	case "content_block_start":
		if response.Index != nil {
			delete(state.pending, *response.Index)
			if response.ContentBlock != nil && response.ContentBlock.Type == "web_search_tool_result" {
				state.pending[*response.Index] = claudeWebSearchResult{id: response.ContentBlock.ToolUseId, billable: billableClaudeWebSearchResult(response.ContentBlock)}
			}
		}
	case "content_block_stop":
		if response.Index != nil {
			if result, ok := state.pending[*response.Index]; ok {
				state.observed = true
				if result.billable {
					state.completed[result.id] = struct{}{}
				}
				delete(state.pending, *response.Index)
			}
		}
	case "message":
		for _, block := range response.Content {
			if block.Type == "web_search_tool_result" {
				state.observed = true
				if billableClaudeWebSearchResult(&block) {
					state.completed[block.ToolUseId] = struct{}{}
				}
			}
		}
	}
	if (response.Type == "message" || response.Type == "message_delta") && response.Usage != nil && response.Usage.ServerToolUse != nil {
		count := max(0, response.Usage.ServerToolUse.WebSearchRequests)
		state.reported = &count
		state.observed = true
	}
	if state.observed {
		count := len(state.completed)
		if state.reported != nil {
			count = *state.reported
		}
		c.Set("claude_web_search_requests", count)
		c.Set("claude_web_search_usage_observed", true)
	}
}

func HandleStreamFinalResponse(c *gin.Context, info *relaycommon.RelayInfo, claudeInfo *ClaudeResponseInfo) *types.NewAPIError {
	finalizeClaudeStreamUsage(c, info, claudeInfo)

	switch info.RelayFormat {
	case types.RelayFormatOpenAI:
		if info.ShouldIncludeUsage {
			openAIUsage := buildOpenAIStyleUsageFromClaudeUsage(claudeInfo.Usage)
			response := helper.GenerateFinalUsageResponse(claudeInfo.ResponseId, claudeInfo.Created, info.UpstreamModelName, openAIUsage)
			if err := sendClaudeConvertedStreamData(c, response); err != nil {
				return err
			}
		}
		if err := c.Request.Context().Err(); err != nil {
			return types.NewError(err, types.ErrorCodeBadResponseBody)
		}
		if _, err := io.WriteString(c.Writer, "data: [DONE]\n\n"); err != nil {
			return types.NewError(err, types.ErrorCodeBadResponseBody)
		}
		if err := helper.FlushWriter(c); err != nil {
			return types.NewError(err, types.ErrorCodeBadResponseBody)
		}
	case types.RelayFormatGemini:
		state, err := claudeToGeminiStreamState(info)
		if err != nil {
			return types.NewError(err, types.ErrorCodeBadResponseBody)
		}
		results, err := service.FinalizeStreamResponse(c, info, state)
		if err != nil {
			return types.NewError(err, types.ErrorCodeBadResponseBody)
		}
		return sendGeminiStreamResults(c, results)
	}
	return nil
}

func finalizeClaudeStreamUsage(c *gin.Context, info *relaycommon.RelayInfo, claudeInfo *ClaudeResponseInfo) {
	if claudeInfo.Usage.CompletionTokens == 0 || !claudeInfo.Done {
		if common.DebugEnabled {
			common.SysLog("claude response usage is not complete, maybe upstream error")
		}
		// 只补缺失字段，不整份覆盖——保留 message_start 已拿到的 cache 字段
		fallback := service.ResponseText2Usage(c, claudeInfo.ResponseText.String(), info.UpstreamModelName, info.GetEstimatePromptTokens())
		if claudeInfo.Usage.CompletionTokens == 0 ||
			(!claudeInfo.Done && fallback.CompletionTokens > claudeInfo.Usage.CompletionTokens) {
			claudeInfo.Usage.CompletionTokens = fallback.CompletionTokens
		}
		if claudeInfo.Usage.PromptTokens == 0 {
			claudeInfo.Usage.PromptTokens = fallback.PromptTokens
		}
		claudeInfo.Usage.TotalTokens = claudeInfo.Usage.PromptTokens + claudeInfo.Usage.CompletionTokens
	}
	if claudeInfo.Usage != nil {
		claudeInfo.Usage.UsageSemantic = "anthropic"
	}
	relayconvert.FinalizeClaudeStreamBillingUsage(claudeInfo)
}

func ClaudeStreamHandler(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (*dto.Usage, *types.NewAPIError) {
	service.ResetClaudeWebSearchBilling(c)
	claudeInfo := &ClaudeResponseInfo{
		ResponseId:   helper.GetResponseID(c),
		Created:      common.GetTimestamp(),
		Model:        info.UpstreamModelName,
		ResponseText: strings.Builder{},
		Usage:        &dto.Usage{},
	}
	var err *types.NewAPIError
	var sawMessageStop bool
	helper.StreamScannerHandler(c, resp, info, func(data string, sr *helper.StreamResult) {
		err = HandleStreamResponseData(c, info, claudeInfo, data)
		if err != nil {
			sr.Stop(err)
			return
		}
		var event struct {
			Type string `json:"type"`
		}
		if common.UnmarshalJsonStr(data, &event) == nil && event.Type == "message_stop" {
			sawMessageStop = true
			if info.RelayFormat == types.RelayFormatClaude {
				sr.DoneAfterDelivery()
			} else {
				// Converted streams still have downstream terminal events to send.
				sr.Done()
			}
		}
	})
	if err == nil && !info.StreamStatus.IsSuccessful() {
		err = types.NewErrorWithStatusCode(fmt.Errorf("upstream Claude stream interrupted: %s", info.StreamStatus.EndReason), types.ErrorCodeBadResponse, http.StatusBadGateway)
	}
	if route, exists := c.Get("protocol_route"); exists && route != nil && err == nil && !sawMessageStop {
		err = types.NewErrorWithStatusCode(fmt.Errorf("upstream Claude stream ended without message_stop"), types.ErrorCodeBadResponse, http.StatusBadGateway)
	}
	if err != nil {
		finalizeClaudeStreamUsage(c, info, claudeInfo)
		if !info.StreamStatus.HasErrors() {
			info.StreamStatus.RecordError(err.Error())
		}
		return claudeInfo.Usage, err
	}

	if err := HandleStreamFinalResponse(c, info, claudeInfo); err != nil {
		info.StreamStatus.RecordError(err.Error())
		return claudeInfo.Usage, err
	}
	return claudeInfo.Usage, nil
}

func HandleClaudeResponseData(c *gin.Context, info *relaycommon.RelayInfo, claudeInfo *ClaudeResponseInfo, httpResp *http.Response, data []byte) *types.NewAPIError {
	service.ResetClaudeWebSearchBilling(c)
	var claudeResponse dto.ClaudeResponse
	err := common.Unmarshal(data, &claudeResponse)
	if err != nil {
		return types.NewError(err, types.ErrorCodeBadResponseBody)
	}
	if claudeError := claudeResponse.GetClaudeError(); claudeError != nil && claudeError.Type != "" {
		return types.WithClaudeError(*claudeError, http.StatusInternalServerError)
	}
	maybeMarkClaudeRefusal(c, claudeResponse.StopReason)
	if claudeInfo.Usage == nil {
		claudeInfo.Usage = &dto.Usage{}
	}
	if claudeResponse.Usage != nil {
		claudeInfo.Usage.PromptTokens = claudeResponse.Usage.InputTokens
		claudeInfo.Usage.CompletionTokens = claudeResponse.Usage.OutputTokens
		claudeInfo.Usage.TotalTokens = claudeResponse.Usage.InputTokens + claudeResponse.Usage.OutputTokens
		claudeInfo.Usage.UsageSemantic = "anthropic"
		claudeInfo.Usage.BillingUsage = dto.CloneBillingUsage(claudeResponse.Usage.BillingUsage)
		if claudeInfo.Usage.BillingUsage == nil {
			claudeInfo.Usage.BillingUsage = dto.NewClaudeMessagesBillingUsage(claudeResponse.Usage)
		}
		claudeInfo.Usage.PromptTokensDetails.CachedTokens = claudeResponse.Usage.CacheReadInputTokens
		claudeInfo.Usage.PromptTokensDetails.CachedCreationTokens = claudeResponse.Usage.CacheCreationInputTokens
		claudeInfo.Usage.ClaudeCacheCreation5mTokens = claudeResponse.Usage.GetCacheCreation5mTokens()
		claudeInfo.Usage.ClaudeCacheCreation1hTokens = claudeResponse.Usage.GetCacheCreation1hTokens()
	}
	var responseData []byte
	switch info.RelayFormat {
	case types.RelayFormatOpenAI:
		openaiResponse := ResponseClaude2OpenAI(&claudeResponse)
		openaiResponse.Usage = buildOpenAIStyleUsageFromClaudeUsage(claudeInfo.Usage)
		responseData, err = common.Marshal(openaiResponse)
		if err != nil {
			return types.NewError(err, types.ErrorCodeBadResponseBody)
		}
	case types.RelayFormatOpenAIResponses:
		convertResult, err := service.ConvertResponse(c, info, types.RelayFormatOpenAIResponses, &claudeResponse)
		if err != nil {
			return types.NewError(err, types.ErrorCodeBadResponseBody)
		}
		responsesResponse, ok := convertResult.Value.(*dto.OpenAIResponsesResponse)
		if !ok {
			return types.NewError(fmt.Errorf("expected OpenAI Responses response, got %T", convertResult.Value), types.ErrorCodeBadResponseBody)
		}
		if responseID := helper.GetResponseID(c); responseID != "" {
			responsesResponse.ID = responseID
		}
		responseData, err = common.Marshal(responsesResponse)
		if err != nil {
			return types.NewError(err, types.ErrorCodeBadResponseBody)
		}
	case types.RelayFormatClaude:
		responseData = data
	case types.RelayFormatGemini:
		{
			convertResult, convertErr := service.ConvertResponse(c, info, types.RelayFormatGemini, &claudeResponse)
			if convertErr != nil {
				return types.NewError(convertErr, types.ErrorCodeBadResponseBody)
			}
			geminiResponse, ok := convertResult.Value.(*dto.GeminiChatResponse)
			if !ok {
				return types.NewError(fmt.Errorf("expected Gemini generateContent response, got %T", convertResult.Value), types.ErrorCodeBadResponseBody)
			}
			responseData, err = common.Marshal(geminiResponse)
			if err != nil {
				return types.NewError(err, types.ErrorCodeBadResponseBody)
			}
		}
	}

	observeClaudeWebSearchUsage(c, &claudeResponse)

	for _, block := range claudeResponse.Content {
		if block.Type == "tool_use" {
			info.CountBillableToolCall(dto.BuildInCallToolUse, block.Name)
		}
	}

	service.IOCopyBytesGracefully(c, httpResp, responseData)
	return nil
}

func ClaudeHandler(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (*dto.Usage, *types.NewAPIError) {
	defer service.CloseResponseBodyGracefully(resp)

	claudeInfo := &ClaudeResponseInfo{
		ResponseId:   helper.GetResponseID(c),
		Created:      common.GetTimestamp(),
		Model:        info.UpstreamModelName,
		ResponseText: strings.Builder{},
		Usage:        &dto.Usage{},
	}
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, types.NewError(err, types.ErrorCodeBadResponseBody)
	}
	logger.LogDebug(c, "responseBody: %s", responseBody)
	handleErr := HandleClaudeResponseData(c, info, claudeInfo, resp, responseBody)
	if handleErr != nil {
		return nil, handleErr
	}
	return claudeInfo.Usage, nil
}
