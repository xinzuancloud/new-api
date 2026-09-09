package oaichat

import (
	"testing"

	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/relayconvert/convmeta"
	"github.com/samber/lo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestChatCompletionsResponseToResponsesPreservesTextToolCallsAndUsage(t *testing.T) {
	chat := &dto.OpenAITextResponse{
		Id:      "chatcmpl_1",
		Model:   "gpt-test",
		Created: 456,
		Choices: []dto.OpenAITextResponseChoice{
			{
				Message:      assistantMessageWithTool("I will call.", "call_1", "lookup", `{"q":"x"}`),
				FinishReason: "tool_calls",
			},
		},
		Usage: dto.Usage{PromptTokens: 3, CompletionTokens: 5, TotalTokens: 8},
	}

	resp, usage, err := ChatCompletionsResponseToResponsesResponse(chat, "resp_1")
	require.NoError(t, err)
	require.NotNil(t, usage)

	assert.Equal(t, "resp_1", resp.ID)
	assert.Equal(t, "response", resp.Object)
	assert.Equal(t, `"completed"`, string(resp.Status))
	assert.Equal(t, 3, resp.Usage.InputTokens)
	assert.Equal(t, 5, resp.Usage.OutputTokens)
	require.Len(t, resp.Output, 2)
	assert.Equal(t, responsesOutputTypeMessage, resp.Output[0].Type)
	assert.Equal(t, "I will call.", resp.Output[0].Content[0].Text)
	assert.Equal(t, responsesOutputTypeFunctionCall, resp.Output[1].Type)
	assert.Equal(t, "call_1", resp.Output[1].CallId)
	assert.Equal(t, "lookup", resp.Output[1].Name)
	assert.Equal(t, `"{\"q\":\"x\"}"`, string(resp.Output[1].Arguments))
}

func TestChatCompletionsResponseToResponsesRestoresResponsesLiteTools(t *testing.T) {
	bridge, err := convmeta.NewResponsesLiteBridge([]convmeta.ResponsesLiteTool{
		{Alias: "newapi_lite_0_exec", Kind: "custom", Namespace: "functions", Name: "exec"},
		{Alias: "newapi_lite_1_wait", Kind: "function", Namespace: "functions", Name: "wait"},
	})
	require.NoError(t, err)
	message := dto.Message{Role: "assistant"}
	message.SetToolCalls([]dto.ToolCallRequest{
		{ID: "call_exec", Type: "function", Function: dto.FunctionRequest{Name: "newapi_lite_0_exec", Arguments: `{"input":"text('ok')"}`}},
		{ID: "call_wait", Type: "function", Function: dto.FunctionRequest{Name: "newapi_lite_1_wait", Arguments: `{"cell_id":"1"}`}},
	})
	chat := &dto.OpenAITextResponse{
		Id:    "chatcmpl_1",
		Model: "model",
		Choices: []dto.OpenAITextResponseChoice{{
			Message:      message,
			FinishReason: "tool_calls",
		}},
	}

	resp, _, err := ChatCompletionsResponseToResponsesResponse(chat, "resp_1")
	require.NoError(t, err)
	require.NoError(t, RestoreResponsesLiteResponse(resp, bridge))
	require.Len(t, resp.Output, 2)
	assert.Equal(t, "custom_tool_call", resp.Output[0].Type)
	assert.Equal(t, "functions", resp.Output[0].Namespace)
	assert.Equal(t, "exec", resp.Output[0].Name)
	assert.Equal(t, "text('ok')", resp.Output[0].Input)
	assert.Empty(t, resp.Output[0].Arguments)
	assert.Equal(t, responsesOutputTypeFunctionCall, resp.Output[1].Type)
	assert.Equal(t, "functions", resp.Output[1].Namespace)
	assert.Equal(t, "wait", resp.Output[1].Name)
	assert.Equal(t, `"{\"cell_id\":\"1\"}"`, string(resp.Output[1].Arguments))
}

func TestChatCompletionsStreamToResponsesRestoresResponsesLiteCustomToolEvents(t *testing.T) {
	bridge, err := convmeta.NewResponsesLiteBridge([]convmeta.ResponsesLiteTool{
		{Alias: "newapi_lite_0_exec", Kind: "custom", Namespace: "functions", Name: "exec"},
	})
	require.NoError(t, err)
	state := NewChatToResponsesStreamState("resp_1", "model")
	state.SetResponsesLiteBridge(bridge)
	toolIndex := 0

	events := mustResponsesEventsFromChatChunk(t, state, &dto.ChatCompletionsStreamResponse{Choices: []dto.ChatCompletionsStreamResponseChoice{{
		Delta: dto.ChatCompletionsStreamResponseChoiceDelta{ToolCalls: []dto.ToolCallResponse{{Index: &toolIndex, ID: "call_exec", Type: "function", Function: dto.FunctionResponse{Arguments: `{"input":"text('ok')"}`}}}},
	}}})
	for _, event := range events {
		assert.NotEqual(t, responsesEventFunctionArgsDelta, event.Type)
	}
	events = append(events, mustResponsesEventsFromChatChunk(t, state, &dto.ChatCompletionsStreamResponse{Choices: []dto.ChatCompletionsStreamResponseChoice{{
		Delta: dto.ChatCompletionsStreamResponseChoiceDelta{ToolCalls: []dto.ToolCallResponse{{Index: &toolIndex, Function: dto.FunctionResponse{Name: "newapi_lite_0_exec"}}}},
	}}})...)
	finishReason := "tool_calls"
	events = append(events, mustResponsesEventsFromChatChunk(t, state, &dto.ChatCompletionsStreamResponse{Choices: []dto.ChatCompletionsStreamResponseChoice{{FinishReason: &finishReason}}})...)
	events = append(events, FinalizeChatCompletionsStreamToResponses(state)...)

	var eventTypes []string
	for _, event := range events {
		eventTypes = append(eventTypes, event.Type)
	}
	assert.Contains(t, eventTypes, "response.custom_tool_call_input.delta")
	assert.Contains(t, eventTypes, "response.custom_tool_call_input.done")
	for _, event := range events {
		if event.Type == responsesEventOutputItemDone && event.Payload.Item != nil && event.Payload.Item.CallId == "call_exec" {
			assert.Equal(t, "custom_tool_call", event.Payload.Item.Type)
			assert.Equal(t, "functions", event.Payload.Item.Namespace)
			assert.Equal(t, "exec", event.Payload.Item.Name)
			assert.Equal(t, "text('ok')", event.Payload.Item.Input)
		}
	}
}

func TestChatCompletionsResponseToResponsesEmitsReasoningSummaryBeforeText(t *testing.T) {
	message := dto.Message{Role: "assistant", Content: "final answer"}
	message.ReasoningContent = lo.ToPtr("thinking summary")
	resp, _, err := ChatCompletionsResponseToResponsesResponse(&dto.OpenAITextResponse{
		Id:    "chatcmpl_1",
		Model: "gpt-test",
		Choices: []dto.OpenAITextResponseChoice{
			{Message: message, FinishReason: "stop"},
		},
	}, "resp_1")
	require.NoError(t, err)

	require.Len(t, resp.Output, 2)
	assert.Equal(t, responsesOutputTypeReasoning, resp.Output[0].Type)
	require.Len(t, resp.Output[0].Summary, 1)
	assert.Equal(t, "thinking summary", resp.Output[0].Summary[0].Text)
	assert.Empty(t, resp.Output[0].Content)
	assert.Equal(t, responsesOutputTypeMessage, resp.Output[1].Type)
	assert.Equal(t, "final answer", resp.Output[1].Content[0].Text)
}

func TestChatCompletionsResponseToResponsesMapsIncompleteFinishReasons(t *testing.T) {
	tests := []struct {
		name         string
		finishReason string
		wantReason   string
	}{
		{name: "length", finishReason: "length", wantReason: responsesIncompleteReasonMaxTokens},
		{name: "content filter", finishReason: "content_filter", wantReason: responsesIncompleteReasonContentFilter},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, _, err := ChatCompletionsResponseToResponsesResponse(&dto.OpenAITextResponse{
				Id:    "chatcmpl_1",
				Model: "gpt-test",
				Choices: []dto.OpenAITextResponseChoice{
					{
						Message:      dto.Message{Role: "assistant", Content: "partial"},
						FinishReason: tt.finishReason,
					},
				},
			}, "resp_1")
			require.NoError(t, err)

			assert.Equal(t, `"incomplete"`, string(resp.Status))
			require.NotNil(t, resp.IncompleteDetails)
			assert.Equal(t, tt.wantReason, resp.IncompleteDetails.Reason)
			require.Len(t, resp.Output, 1)
			assert.Equal(t, "incomplete", resp.Output[0].Status)
		})
	}
}

func TestChatCompletionsStreamToResponsesEventsAggregatesUsageAndToolArgs(t *testing.T) {
	state := NewChatToResponsesStreamState("resp_1", "gpt-test")
	state.Created = 123
	toolIndex := 0

	var events []ChatToResponsesStreamEvent
	events = append(events, mustResponsesEventsFromChatChunk(t, state, &dto.ChatCompletionsStreamResponse{
		Id:      "chatcmpl_1",
		Model:   "gpt-test",
		Created: 123,
		Choices: []dto.ChatCompletionsStreamResponseChoice{
			{Index: 0, Delta: dto.ChatCompletionsStreamResponseChoiceDelta{Role: "assistant"}},
		},
	})...)
	events = append(events, mustResponsesEventsFromChatChunk(t, state, &dto.ChatCompletionsStreamResponse{
		Choices: []dto.ChatCompletionsStreamResponseChoice{
			{Index: 0, Delta: dto.ChatCompletionsStreamResponseChoiceDelta{Content: lo.ToPtr("hello")}},
		},
	})...)
	events = append(events, mustResponsesEventsFromChatChunk(t, state, &dto.ChatCompletionsStreamResponse{
		Choices: []dto.ChatCompletionsStreamResponseChoice{
			{Index: 0, Delta: dto.ChatCompletionsStreamResponseChoiceDelta{ToolCalls: []dto.ToolCallResponse{
				{Index: &toolIndex, ID: "call_1", Type: "function", Function: dto.FunctionResponse{Name: "lookup"}},
			}}},
		},
	})...)
	events = append(events, mustResponsesEventsFromChatChunk(t, state, &dto.ChatCompletionsStreamResponse{
		Choices: []dto.ChatCompletionsStreamResponseChoice{
			{Index: 0, Delta: dto.ChatCompletionsStreamResponseChoiceDelta{ToolCalls: []dto.ToolCallResponse{
				{Index: &toolIndex, Function: dto.FunctionResponse{Arguments: `{"q":"x"}`}},
			}}},
		},
	})...)
	finishReason := "tool_calls"
	events = append(events, mustResponsesEventsFromChatChunk(t, state, &dto.ChatCompletionsStreamResponse{
		Choices: []dto.ChatCompletionsStreamResponseChoice{
			{Index: 0, FinishReason: &finishReason},
		},
	})...)
	events = append(events, mustResponsesEventsFromChatChunk(t, state, &dto.ChatCompletionsStreamResponse{
		Usage: &dto.Usage{PromptTokens: 2, CompletionTokens: 4, TotalTokens: 6},
	})...)
	events = append(events, FinalizeChatCompletionsStreamToResponses(state)...)

	require.Len(t, events, 10)
	assert.Equal(t, responsesEventCreated, events[0].Type)
	assert.Equal(t, responsesEventOutputTextDelta, events[2].Type)
	assert.Equal(t, "hello", events[2].Payload.Delta)
	assert.Equal(t, responsesEventFunctionArgsDelta, events[4].Type)
	assert.Equal(t, `{"q":"x"}`, events[4].Payload.Delta)
	assert.Equal(t, responsesEventCompleted, events[9].Type)
	require.NotNil(t, events[9].Payload.Response)
	assert.Equal(t, 6, events[9].Payload.Response.Usage.TotalTokens)
	require.Len(t, events[9].Payload.Response.Output, 2)
	assert.Equal(t, "hello", events[9].Payload.Response.Output[0].Content[0].Text)
	assert.Equal(t, `"{\"q\":\"x\"}"`, string(events[9].Payload.Response.Output[1].Arguments))
}

func mustResponsesEventsFromChatChunk(t *testing.T, state *ChatToResponsesStreamState, chunk *dto.ChatCompletionsStreamResponse) []ChatToResponsesStreamEvent {
	t.Helper()
	events, err := ChatCompletionsStreamChunkToResponsesEvents(chunk, state)
	require.NoError(t, err)
	return events
}
