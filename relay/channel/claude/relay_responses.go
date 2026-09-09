package claude

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/relayconvert"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

func ClaudeResponsesStreamHandler(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (usage *dto.Usage, apiErr *types.NewAPIError) {
	service.ResetClaudeWebSearchBilling(c)
	responseID := helper.GetResponseID(c)
	created := common.GetTimestamp()
	state, err := relayconvert.NewResponseStreamState(types.RelayFormatClaude, types.RelayFormatOpenAIResponses, relayconvert.ResponseStreamOptions{
		ID:                 responseID,
		Model:              info.UpstreamModelName,
		Created:            created,
		EmitSequenceNumber: true,
	})
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponse, http.StatusInternalServerError)
	}
	hostedBridge := relayconvert.NewClaudeHostedStreamBridge()

	claudeInfo := &ClaudeResponseInfo{
		ResponseId:   responseID,
		Created:      created,
		Model:        info.UpstreamModelName,
		ResponseText: strings.Builder{},
		Usage:        &dto.Usage{},
	}
	var streamErr *types.NewAPIError
	var sawMessageStop bool
	route, routeExists := c.Get("protocol_route")
	protocolRouting := routeExists && route != nil
	defer func() {
		if protocolRouting && apiErr != nil {
			finalizeClaudeStreamUsage(c, info, claudeInfo)
			usage = claudeInfo.Usage
			if info.StreamStatus != nil && !info.StreamStatus.HasErrors() {
				info.StreamStatus.RecordError(apiErr.Error())
			}
		}
	}()
	// streamFailed means a Responses-native terminal error was sent successfully.
	// Legacy routing treats that event as handled; configured routing also returns
	// an error while preserving partial usage for settlement.
	streamFailed := false

	sendResponsesEvent := func(eventType string, payload dto.ResponsesStreamResponse) bool {
		payload.Type = eventType
		data, err := common.Marshal(payload)
		if err != nil {
			streamErr = types.NewOpenAIError(err, types.ErrorCodeJsonMarshalFailed, http.StatusInternalServerError)
			return false
		}
		if err := helper.ResponseChunkData(c, dto.ResponsesStreamResponse{Type: eventType}, string(data)); err != nil {
			streamErr = types.NewOpenAIError(err, types.ErrorCodeBadResponse, http.StatusInternalServerError)
			return false
		}
		if protocolRouting && eventType == "response.failed" {
			c.Set("protocol_stream_error_written", true)
		}
		return true
	}
	sendResult := func(result relayconvert.ResponseResult) bool {
		event, ok := result.Value.(relayconvert.ChatToResponsesStreamEvent)
		if !ok {
			streamErr = types.NewOpenAIError(
				fmt.Errorf("expected OpenAI Responses stream event, got %T", result.Value),
				types.ErrorCodeBadResponse,
				http.StatusInternalServerError,
			)
			return false
		}
		return sendResponsesEvent(event.Type, event.Payload)
	}
	failResponsesStream := func(err error) bool {
		if protocolRouting && streamErr == nil {
			streamErr = types.NewOpenAIError(err, types.ErrorCodeBadResponse, http.StatusBadGateway)
		}
		failureResults, handled := state.FailResponsesStream("server_error", err.Error(), "")
		if !handled {
			return false
		}
		for _, result := range failureResults {
			if !sendResult(result) {
				return true
			}
		}
		streamFailed = true
		return true
	}

	helper.StreamScannerHandler(c, resp, info, func(data string, sr *helper.StreamResult) {
		var claudeResponse dto.ClaudeResponse
		if err := common.UnmarshalJsonStr(data, &claudeResponse); err != nil {
			logger.LogError(c, "failed to unmarshal Claude stream event: "+err.Error())
			if failResponsesStream(err) {
				// Legacy routing keeps its protocol-level failure behavior; configured
				// routes also return the error for partial settlement and diagnostics.
				sr.Stop(streamErr)
				return
			}
			streamErr = types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
			sr.Stop(streamErr)
			return
		}
		if claudeError := claudeResponse.GetClaudeError(); claudeError != nil && claudeError.Type != "" {
			if protocolRouting {
				streamErr = types.WithClaudeError(*claudeError, http.StatusBadGateway)
			}
			if failResponsesStream(fmt.Errorf("%s", claudeError.Message)) {
				sr.Stop(streamErr)
				return
			}
			streamErr = types.WithClaudeError(*claudeError, http.StatusInternalServerError)
			sr.Stop(streamErr)
			return
		}

		if claudeResponse.Type == "message_stop" {
			sawMessageStop = true
		}
		if claudeResponse.StopReason != "" {
			maybeMarkClaudeRefusal(c, claudeResponse.StopReason)
		}
		if claudeResponse.Delta != nil && claudeResponse.Delta.StopReason != nil {
			maybeMarkClaudeRefusal(c, *claudeResponse.Delta.StopReason)
		}
		if claudeResponse.Type == "message_start" && claudeResponse.Message != nil {
			info.UpstreamModelName = claudeResponse.Message.Model
		}
		FormatClaudeResponseInfo(&claudeResponse, nil, claudeInfo)
		observeClaudeWebSearchUsage(c, &claudeResponse)
		countClaudeStreamBillableTools(c, info, &claudeResponse)
		hostedEvents, consumed, err := hostedBridge.Convert(&claudeResponse, state)
		if err != nil {
			if failResponsesStream(err) {
				sr.Stop(streamErr)
				return
			}
			streamErr = types.NewOpenAIError(err, types.ErrorCodeBadResponse, http.StatusInternalServerError)
			sr.Stop(streamErr)
			return
		}
		for _, event := range hostedEvents {
			if !sendResponsesEvent(event.Type, event.Payload) {
				sr.Stop(streamErr)
				return
			}
		}
		if consumed {
			if claudeResponse.Type == "message_stop" {
				sr.Done()
			}
			return
		}

		results, err := service.ConvertStreamResponseChunk(c, info, state, &claudeResponse)
		if err != nil {
			if failResponsesStream(err) {
				sr.Stop(streamErr)
				return
			}
			streamErr = types.NewOpenAIError(err, types.ErrorCodeBadResponse, http.StatusInternalServerError)
			sr.Stop(streamErr)
			return
		}
		for _, result := range results {
			if !sendResult(result) {
				sr.Stop(streamErr)
				return
			}
		}
		if claudeResponse.Type == "message_stop" {
			// Responses completion is emitted after conversion finalization.
			sr.Done()
		}
	})
	if protocolRouting && streamErr == nil {
		if !info.StreamStatus.IsSuccessful() {
			streamErr = types.NewOpenAIError(fmt.Errorf("upstream Claude stream interrupted: %s", info.StreamStatus.EndReason), types.ErrorCodeBadResponse, http.StatusBadGateway)
		} else if !sawMessageStop {
			streamErr = types.NewOpenAIError(fmt.Errorf("upstream Claude stream ended without message_stop"), types.ErrorCodeBadResponse, http.StatusBadGateway)
		}
		if streamErr != nil {
			finalizeClaudeStreamUsage(c, info, claudeInfo)
			openAIUsage := buildOpenAIStyleUsageFromClaudeUsage(claudeInfo.Usage)
			state.SetUsage(&openAIUsage)
			failResponsesStream(streamErr)
		}
	}
	if streamErr != nil {
		return nil, streamErr
	}
	if streamFailed {
		return claudeInfo.Usage, nil
	}

	if err := HandleStreamFinalResponse(c, info, claudeInfo); err != nil {
		return claudeInfo.Usage, err
	}
	openAIUsage := buildOpenAIStyleUsageFromClaudeUsage(claudeInfo.Usage)
	state.SetUsage(&openAIUsage)
	finalResults, err := service.FinalizeStreamResponse(c, info, state)
	if err != nil {
		if failResponsesStream(err) {
			return claudeInfo.Usage, streamErr
		}
		return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponse, http.StatusInternalServerError)
	}
	for _, result := range finalResults {
		if !sendResult(result) {
			return nil, streamErr
		}
	}
	return claudeInfo.Usage, nil
}
