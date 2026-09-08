package claude

import (
	"fmt"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHandleClaudeResponseDataCountsToolUse(t *testing.T) {
	gin.SetMode(gin.TestMode)
	operation_setting.SetToolPriceForTest("lookup_fn", 3.0)
	t.Cleanup(func() {
		operation_setting.DeleteToolPriceForTest("lookup_fn")
	})

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	info := &relaycommon.RelayInfo{
		OriginModelName: "claude-3-7-sonnet",
		RelayFormat:     types.RelayFormatClaude,
	}
	claudeInfo := &ClaudeResponseInfo{Usage: &dto.Usage{}}

	data := []byte(`{
		"type":"message",
		"content":[
			{"type":"text","text":"hi"},
			{"type":"tool_use","id":"tu1","name":"lookup_fn","input":{}},
			{"type":"server_tool_use","id":"stu1","name":"web_search","input":{}}
		],
		"usage":{"input_tokens":1,"output_tokens":1}
	}`)

	err := HandleClaudeResponseData(c, info, claudeInfo, nil, data)
	require.Nil(t, err)
	require.NotNil(t, info.ResponsesUsageInfo)
	require.Contains(t, info.ResponsesUsageInfo.BuiltInTools, "lookup_fn")
	assert.Equal(t, 1, info.ResponsesUsageInfo.BuiltInTools["lookup_fn"].CallCount)
	assert.NotContains(t, info.ResponsesUsageInfo.BuiltInTools, "web_search")
}

func TestCountClaudeStreamBillableToolsSetsWebSearchRequests(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	info := &relaycommon.RelayInfo{OriginModelName: "claude-3-7-sonnet"}

	observeClaudeWebSearchUsage(c, &dto.ClaudeResponse{
		Type: "message_delta",
		Usage: &dto.ClaudeUsage{
			ServerToolUse: &dto.ClaudeServerToolUse{WebSearchRequests: 3},
		},
	})
	assert.Equal(t, 3, c.GetInt("claude_web_search_requests"))

	operation_setting.SetToolPriceForTest("stream_fn", 2.0)
	t.Cleanup(func() {
		operation_setting.DeleteToolPriceForTest("stream_fn")
	})
	countClaudeStreamBillableTools(c, info, &dto.ClaudeResponse{
		Type: "content_block_start",
		ContentBlock: &dto.ClaudeMediaMessage{
			Type: "tool_use",
			Name: "stream_fn",
		},
	})
	require.Contains(t, info.ResponsesUsageInfo.BuiltInTools, "stream_fn")
	assert.Equal(t, 1, info.ResponsesUsageInfo.BuiltInTools["stream_fn"].CallCount)
}

func TestClaudeWebSearchBillingWithoutServerStatistics(t *testing.T) {
	gin.SetMode(gin.TestMode)
	results := []dto.ClaudeMediaMessage{
		{Type: "web_search_tool_result", ToolUseId: "search_1", Content: []any{map[string]any{"type": "web_search_result", "url": "https://example.com"}}},
		{Type: "web_search_tool_result", ToolUseId: "search_1", Content: []any{}}, // Repeated event, same invocation.
		{Type: "web_search_tool_result", ToolUseId: "search_failed", Content: map[string]any{"type": "web_search_tool_result_error", "error_code": "unavailable"}},
		{Type: "web_search_tool_result", ToolUseId: "search_empty", Content: []any{}},
		{Type: "web_search_tool_result", ToolUseId: "search_outer_error", Content: []any{}, IsError: common.GetPointer(true)},
		{Type: "web_search_tool_result", ToolUseId: "search_error_code", Content: []any{}, ErrorCode: "unavailable"},
		{Type: "server_tool_use", Id: "search_pending", Name: "web_search"},
	}
	for _, tc := range []struct {
		name     string
		reported *dto.ClaudeServerToolUse
		blocks   []dto.ClaudeMediaMessage
		want     int
	}{
		{"successful distinct results", nil, results, 2},
		{"reported usage authoritative", &dto.ClaudeServerToolUse{WebSearchRequests: 3}, results, 3},
		{"reported zero authoritative", &dto.ClaudeServerToolUse{}, results, 0},
		{"no completed result", nil, results[4:], 0},
	} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", tc.name, stream), func(t *testing.T) {
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				c.Request = httptest.NewRequest("POST", "/v1/messages", nil)
				info := &relaycommon.RelayInfo{OriginModelName: "fixture-model", RelayFormat: types.RelayFormatClaude}
				usage := &dto.ClaudeUsage{InputTokens: 1, OutputTokens: 1, ServerToolUse: tc.reported}
				if stream {
					observeClaudeWebSearchUsage(c, &dto.ClaudeResponse{Type: "message_start", Usage: &dto.ClaudeUsage{ServerToolUse: &dto.ClaudeServerToolUse{}}})
					for index, block := range tc.blocks {
						observeClaudeWebSearchUsage(c, &dto.ClaudeResponse{Type: "content_block_start", Index: &index, ContentBlock: &block})
						observeClaudeWebSearchUsage(c, &dto.ClaudeResponse{Type: "content_block_stop", Index: &index})
					}
					unfinished := dto.ClaudeMediaMessage{Type: "web_search_tool_result", ToolUseId: "unfinished", Content: []any{}}
					observeClaudeWebSearchUsage(c, &dto.ClaudeResponse{Type: "content_block_start", Index: common.GetPointer(99), ContentBlock: &unfinished})
					observeClaudeWebSearchUsage(c, &dto.ClaudeResponse{Type: "message_delta", Usage: usage})
				} else {
					data, err := common.Marshal(dto.ClaudeResponse{Type: "message", Content: tc.blocks, Usage: usage})
					require.NoError(t, err)
					require.Nil(t, HandleClaudeResponseData(c, info, &ClaudeResponseInfo{Usage: &dto.Usage{}}, nil, data))
				}
				assert.Equal(t, tc.want, c.GetInt("claude_web_search_requests"))
				// A later upstream attempt reuses the request context, not its fee count.
				require.Nil(t, HandleClaudeResponseData(c, info, &ClaudeResponseInfo{Usage: &dto.Usage{}}, nil, []byte(`{"type":"message","content":[],"usage":{"input_tokens":1,"output_tokens":1}}`)))
				assert.Zero(t, c.GetInt("claude_web_search_requests"))
			})
		}
	}
}
