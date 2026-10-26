package sampling

import (
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/docker/docker-agent/pkg/chat"
	"github.com/docker/docker-agent/pkg/tools"
)

func TestSamplingMessagesToChat(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		req     *mcp.CreateMessageParams
		want    []chat.Message
		wantErr bool
	}{
		{
			name: "single user text message",
			req: &mcp.CreateMessageParams{
				Messages: []*mcp.SamplingMessage{
					{Role: "user", Content: &mcp.TextContent{Text: "hello"}},
				},
			},
			want: []chat.Message{
				{Role: chat.MessageRoleUser, Content: "hello"},
			},
		},
		{
			name: "system prompt is prepended",
			req: &mcp.CreateMessageParams{
				SystemPrompt: "be terse",
				Messages: []*mcp.SamplingMessage{
					{Role: "user", Content: &mcp.TextContent{Text: "hi"}},
				},
			},
			want: []chat.Message{
				{Role: chat.MessageRoleSystem, Content: "be terse"},
				{Role: chat.MessageRoleUser, Content: "hi"},
			},
		},
		{
			name: "user and assistant turns are preserved",
			req: &mcp.CreateMessageParams{
				Messages: []*mcp.SamplingMessage{
					{Role: "user", Content: &mcp.TextContent{Text: "ping"}},
					{Role: "assistant", Content: &mcp.TextContent{Text: "pong"}},
					{Role: "user", Content: &mcp.TextContent{Text: "again"}},
				},
			},
			want: []chat.Message{
				{Role: chat.MessageRoleUser, Content: "ping"},
				{Role: chat.MessageRoleAssistant, Content: "pong"},
				{Role: chat.MessageRoleUser, Content: "again"},
			},
		},
		{
			name: "image content becomes a data URL multi-part",
			req: &mcp.CreateMessageParams{
				Messages: []*mcp.SamplingMessage{
					{
						Role:    "user",
						Content: &mcp.ImageContent{Data: []byte("PNGBYTES"), MIMEType: "image/png"},
					},
				},
			},
			want: []chat.Message{
				{
					Role: chat.MessageRoleUser,
					MultiContent: []chat.MessagePart{{
						Type: chat.MessagePartTypeImageURL,
						ImageURL: &chat.MessageImageURL{
							URL: "data:image/png;base64,UE5HQllURVM=",
						},
					}},
				},
			},
		},
		{
			name: "audio content falls back to a text placeholder",
			req: &mcp.CreateMessageParams{
				Messages: []*mcp.SamplingMessage{
					{Role: "user", Content: &mcp.AudioContent{Data: []byte("WAV"), MIMEType: "audio/wav"}},
				},
			},
			want: []chat.Message{
				{Role: chat.MessageRoleUser, Content: "[audio attachment (audio/wav, 3 bytes) — not inlined]"},
			},
		},
		{
			name: "missing role defaults to user",
			req: &mcp.CreateMessageParams{
				Messages: []*mcp.SamplingMessage{
					{Content: &mcp.TextContent{Text: "anonymous"}},
				},
			},
			want: []chat.Message{
				{Role: chat.MessageRoleUser, Content: "anonymous"},
			},
		},
		{
			name: "unsupported role surfaces as an error",
			req: &mcp.CreateMessageParams{
				Messages: []*mcp.SamplingMessage{
					{Role: "tool", Content: &mcp.TextContent{Text: "nope"}},
				},
			},
			wantErr: true,
		},
		{
			name:    "empty request is rejected",
			req:     &mcp.CreateMessageParams{},
			wantErr: true,
		},
		{
			name: "system-prompt-only request is rejected",
			req: &mcp.CreateMessageParams{
				SystemPrompt: "no messages, only a system prompt",
			},
			wantErr: true,
		},
		{
			name: "nil message entry is rejected",
			req: &mcp.CreateMessageParams{
				Messages: []*mcp.SamplingMessage{nil},
			},
			wantErr: true,
		},
		{
			name: "oversize text block is rejected",
			req: &mcp.CreateMessageParams{
				Messages: []*mcp.SamplingMessage{
					{Role: "user", Content: &mcp.TextContent{Text: strings.Repeat("a", maxSamplingTextBytes+1)}},
				},
			},
			wantErr: true,
		},
		{
			name: "oversize image block is rejected",
			req: &mcp.CreateMessageParams{
				Messages: []*mcp.SamplingMessage{
					{Role: "user", Content: &mcp.ImageContent{Data: make([]byte, maxSamplingBinaryBytes+1), MIMEType: "image/png"}},
				},
			},
			wantErr: true,
		},
		{
			name: "oversize system prompt is rejected",
			req: &mcp.CreateMessageParams{
				SystemPrompt: strings.Repeat("a", maxSamplingTextBytes+1),
				Messages: []*mcp.SamplingMessage{
					{Role: "user", Content: &mcp.TextContent{Text: "hi"}},
				},
			},
			wantErr: true,
		},
		{
			name: "too many messages is rejected",
			req: &mcp.CreateMessageParams{
				Messages: tooManyMessages(maxSamplingMessages + 1),
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := samplingMessagesToChat(tt.req)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func tooManyMessages(n int) []*mcp.SamplingMessage {
	out := make([]*mcp.SamplingMessage, n)
	for i := range out {
		out[i] = &mcp.SamplingMessage{Role: "user", Content: &mcp.TextContent{Text: "x"}}
	}
	return out
}

func TestStopReasonMapping(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in   chat.FinishReason
		want string
	}{
		{chat.FinishReasonStop, "endTurn"},
		{chat.FinishReasonLength, "maxTokens"},
		{chat.FinishReasonToolCalls, "toolUse"},
		{chat.FinishReasonNull, "endTurn"},
		{"", "endTurn"},
	}

	for _, tt := range tests {
		t.Run(string(tt.in), func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, stopReason(tt.in))
		})
	}
}

func TestDataURL(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "data:image/png;base64,UE5HQllURVM=", dataURL("image/png", []byte("PNGBYTES")))
	assert.Equal(t, "data:application/octet-stream;base64,YQ==", dataURL("", []byte("a")))
}

func TestSamplingMessagesV2ToChat(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		req     *mcp.CreateMessageWithToolsParams
		want    []chat.Message
		wantErr bool
	}{
		{
			name: "single user text block",
			req: &mcp.CreateMessageWithToolsParams{
				Messages: []*mcp.SamplingMessageV2{
					{Role: "user", Content: []mcp.Content{&mcp.TextContent{Text: "hello"}}},
				},
			},
			want: []chat.Message{
				{Role: chat.MessageRoleUser, Content: "hello"},
			},
		},
		{
			name: "system prompt is prepended",
			req: &mcp.CreateMessageWithToolsParams{
				SystemPrompt: "be terse",
				Messages: []*mcp.SamplingMessageV2{
					{Role: "user", Content: []mcp.Content{&mcp.TextContent{Text: "hi"}}},
				},
			},
			want: []chat.Message{
				{Role: chat.MessageRoleSystem, Content: "be terse"},
				{Role: chat.MessageRoleUser, Content: "hi"},
			},
		},
		{
			name: "multiple text blocks are concatenated",
			req: &mcp.CreateMessageWithToolsParams{
				Messages: []*mcp.SamplingMessageV2{
					{Role: "user", Content: []mcp.Content{
						&mcp.TextContent{Text: "first"},
						&mcp.TextContent{Text: "second"},
					}},
				},
			},
			want: []chat.Message{
				{Role: chat.MessageRoleUser, Content: "first\nsecond"},
			},
		},
		{
			name: "text and image in one message",
			req: &mcp.CreateMessageWithToolsParams{
				Messages: []*mcp.SamplingMessageV2{
					{Role: "user", Content: []mcp.Content{
						&mcp.TextContent{Text: "describe"},
						&mcp.ImageContent{Data: []byte("PNG"), MIMEType: "image/png"},
					}},
				},
			},
			want: []chat.Message{
				{
					Role:    chat.MessageRoleUser,
					Content: "describe",
					MultiContent: []chat.MessagePart{{
						Type:     chat.MessagePartTypeImageURL,
						ImageURL: &chat.MessageImageURL{URL: "data:image/png;base64,UE5H"},
					}},
				},
			},
		},
		{
			name: "tool_use becomes assistant ToolCalls",
			req: &mcp.CreateMessageWithToolsParams{
				Messages: []*mcp.SamplingMessageV2{
					{Role: "assistant", Content: []mcp.Content{
						&mcp.ToolUseContent{
							ID:    "call_1",
							Name:  "get_weather",
							Input: map[string]any{"city": "Paris"},
						},
					}},
				},
			},
			want: []chat.Message{
				{
					Role: chat.MessageRoleAssistant,
					ToolCalls: []tools.ToolCall{{
						ID:   "call_1",
						Type: "function",
						Function: tools.FunctionCall{
							Name:      "get_weather",
							Arguments: `{"city":"Paris"}`,
						},
					}},
				},
			},
		},
		{
			name: "tool_result expands to tool-role message",
			req: &mcp.CreateMessageWithToolsParams{
				Messages: []*mcp.SamplingMessageV2{
					{Role: "user", Content: []mcp.Content{
						&mcp.ToolResultContent{
							ToolUseID: "call_1",
							Content:   []mcp.Content{&mcp.TextContent{Text: "sunny, 22C"}},
						},
					}},
				},
			},
			want: []chat.Message{
				{
					Role:       chat.MessageRoleTool,
					Content:    "sunny, 22C",
					ToolCallID: "call_1",
				},
			},
		},
		{
			name: "tool_result IsError surfaces",
			req: &mcp.CreateMessageWithToolsParams{
				Messages: []*mcp.SamplingMessageV2{
					{Role: "user", Content: []mcp.Content{
						&mcp.ToolResultContent{
							ToolUseID: "call_1",
							Content:   []mcp.Content{&mcp.TextContent{Text: "no such city"}},
							IsError:   true,
						},
					}},
				},
			},
			want: []chat.Message{
				{
					Role:       chat.MessageRoleTool,
					Content:    "no such city",
					ToolCallID: "call_1",
					IsError:    true,
				},
			},
		},
		{
			name: "parallel tool_results expand to multiple rows",
			req: &mcp.CreateMessageWithToolsParams{
				Messages: []*mcp.SamplingMessageV2{
					{Role: "user", Content: []mcp.Content{
						&mcp.ToolResultContent{ToolUseID: "a", Content: []mcp.Content{&mcp.TextContent{Text: "1"}}},
						&mcp.ToolResultContent{ToolUseID: "b", Content: []mcp.Content{&mcp.TextContent{Text: "2"}}},
					}},
				},
			},
			want: []chat.Message{
				{Role: chat.MessageRoleTool, Content: "1", ToolCallID: "a"},
				{Role: chat.MessageRoleTool, Content: "2", ToolCallID: "b"},
			},
		},
		{
			name: "too many messages is rejected",
			req: &mcp.CreateMessageWithToolsParams{
				Messages: tooManyV2Messages(maxSamplingMessages + 1),
			},
			wantErr: true,
		},
		{
			name: "nil message entry is rejected",
			req: &mcp.CreateMessageWithToolsParams{
				Messages: []*mcp.SamplingMessageV2{nil},
			},
			wantErr: true,
		},
		{
			name: "oversize text block is rejected",
			req: &mcp.CreateMessageWithToolsParams{
				Messages: []*mcp.SamplingMessageV2{
					{Role: "user", Content: []mcp.Content{
						&mcp.TextContent{Text: strings.Repeat("a", maxSamplingTextBytes+1)},
					}},
				},
			},
			wantErr: true,
		},
		{
			name:    "empty messages is rejected",
			req:     &mcp.CreateMessageWithToolsParams{},
			wantErr: true,
		},
		{
			name: "tool_use in non-assistant message is rejected",
			req: &mcp.CreateMessageWithToolsParams{
				Messages: []*mcp.SamplingMessageV2{
					{Role: "user", Content: []mcp.Content{
						&mcp.ToolUseContent{ID: "call_1", Name: "calc", Input: map[string]any{"x": 1}},
					}},
				},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := samplingMessagesV2ToChat(tt.req)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func tooManyV2Messages(n int) []*mcp.SamplingMessageV2 {
	out := make([]*mcp.SamplingMessageV2, n)
	for i := range out {
		out[i] = &mcp.SamplingMessageV2{
			Role:    "user",
			Content: []mcp.Content{&mcp.TextContent{Text: "x"}},
		}
	}
	return out
}

func TestSamplingToolsToChat(t *testing.T) {
	t.Parallel()

	t.Run("nil input returns nil", func(t *testing.T) {
		t.Parallel()
		assert.Nil(t, samplingToolsToChat(nil))
	})

	t.Run("converts and preserves schema", func(t *testing.T) {
		t.Parallel()
		schema := map[string]any{"type": "object"}
		got := samplingToolsToChat([]*mcp.Tool{
			{Name: "lookup", Description: "look up a thing", InputSchema: schema},
			nil, // skipped
			{Name: "other"},
		})
		require.Len(t, got, 2)
		assert.Equal(t, "lookup", got[0].Name)
		assert.Equal(t, "mcp-sampling", got[0].Category)
		assert.Equal(t, "look up a thing", got[0].Description)
		assert.Equal(t, schema, got[0].Parameters)
		assert.NotNil(t, got[0].Handler)
		assert.Equal(t, "other", got[1].Name)
	})

	t.Run("noOp handler returns error result", func(t *testing.T) {
		t.Parallel()
		res, err := noOpSamplingToolHandler(t.Context(), tools.ToolCall{}, tools.NopRuntime{})
		require.NoError(t, err)
		require.NotNil(t, res)
		assert.True(t, res.IsError)
	})
}

func TestBuildSamplingWithToolsContent(t *testing.T) {
	t.Parallel()

	t.Run("text only", func(t *testing.T) {
		t.Parallel()
		got := buildSamplingWithToolsContent("hello world", nil)
		require.Len(t, got, 1)
		text, ok := got[0].(*mcp.TextContent)
		require.True(t, ok)
		assert.Equal(t, "hello world", text.Text)
	})

	t.Run("tool calls only — empty text is dropped", func(t *testing.T) {
		t.Parallel()
		got := buildSamplingWithToolsContent("   ", []tools.ToolCall{
			{ID: "a", Function: tools.FunctionCall{Name: "fn", Arguments: `{"x":1}`}},
		})
		require.Len(t, got, 1)
		tu, ok := got[0].(*mcp.ToolUseContent)
		require.True(t, ok)
		assert.Equal(t, "a", tu.ID)
		assert.Equal(t, "fn", tu.Name)
		assert.Equal(t, map[string]any{"x": float64(1)}, tu.Input)
	})

	t.Run("text plus parallel tool calls", func(t *testing.T) {
		t.Parallel()
		got := buildSamplingWithToolsContent("ok", []tools.ToolCall{
			{ID: "a", Function: tools.FunctionCall{Name: "fn1", Arguments: `{}`}},
			{ID: "b", Function: tools.FunctionCall{Name: "fn2", Arguments: `{}`}},
		})
		require.Len(t, got, 3)
		_, isText := got[0].(*mcp.TextContent)
		_, isToolA := got[1].(*mcp.ToolUseContent)
		_, isToolB := got[2].(*mcp.ToolUseContent)
		assert.True(t, isText)
		assert.True(t, isToolA)
		assert.True(t, isToolB)
	})

	t.Run("malformed JSON args fall back to empty input", func(t *testing.T) {
		t.Parallel()
		got := buildSamplingWithToolsContent("", []tools.ToolCall{
			{ID: "a", Function: tools.FunctionCall{Name: "fn", Arguments: `not json`}},
		})
		require.Len(t, got, 1)
		tu, ok := got[0].(*mcp.ToolUseContent)
		require.True(t, ok)
		assert.Equal(t, map[string]any{}, tu.Input)
	})
}

func TestSamplingWithToolsHandler_LimitRejection(t *testing.T) {
	t.Parallel()

	r := Handler{}
	_, err := r.HandleWithTools(t.Context(), &mcp.CreateMessageWithToolsParams{
		Tools: make([]*mcp.Tool, maxSamplingTools+1),
		Messages: []*mcp.SamplingMessageV2{
			{Role: "user", Content: []mcp.Content{&mcp.TextContent{Text: "hi"}}},
		},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "tools")
}

// fakeStream feeds a fixed sequence of MessageStreamResponse values into
// drainSamplingStreamWithTools for unit testing.
type fakeStream struct {
	responses []chat.MessageStreamResponse
	idx       int
	closed    bool
}

func (f *fakeStream) Recv() (chat.MessageStreamResponse, error) {
	if f.idx >= len(f.responses) {
		return chat.MessageStreamResponse{}, io.EOF
	}
	resp := f.responses[f.idx]
	f.idx++
	return resp, nil
}

func (f *fakeStream) Close() {
	f.closed = true
}

func TestDrainSamplingStreamWithTools(t *testing.T) {
	t.Parallel()

	t.Run("plain text completion", func(t *testing.T) {
		t.Parallel()
		s := &fakeStream{responses: []chat.MessageStreamResponse{
			{Choices: []chat.MessageStreamChoice{{Delta: chat.MessageDelta{Content: "hello "}}}},
			{Choices: []chat.MessageStreamChoice{{Delta: chat.MessageDelta{Content: "world"}, FinishReason: chat.FinishReasonStop}}},
		}}
		text, calls, fr, err := drainSamplingStreamWithTools(s)
		require.NoError(t, err)
		assert.Equal(t, "hello world", text)
		assert.Empty(t, calls)
		assert.Equal(t, chat.FinishReasonStop, fr)
		assert.True(t, s.closed)
	})

	t.Run("tool call aggregation across chunks", func(t *testing.T) {
		t.Parallel()
		s := &fakeStream{responses: []chat.MessageStreamResponse{
			{Choices: []chat.MessageStreamChoice{{Delta: chat.MessageDelta{ToolCalls: []tools.ToolCall{
				{ID: "c1", Type: "function", Function: tools.FunctionCall{Name: "fn", Arguments: `{"a":`}},
			}}}}},
			{Choices: []chat.MessageStreamChoice{{Delta: chat.MessageDelta{ToolCalls: []tools.ToolCall{
				{ID: "c1", Function: tools.FunctionCall{Arguments: `1}`}},
			}}, FinishReason: chat.FinishReasonToolCalls}}},
		}}
		text, calls, fr, err := drainSamplingStreamWithTools(s)
		require.NoError(t, err)
		assert.Empty(t, text)
		require.Len(t, calls, 1)
		assert.Equal(t, "c1", calls[0].ID)
		assert.Equal(t, "fn", calls[0].Function.Name)
		assert.Equal(t, `{"a":1}`, calls[0].Function.Arguments)
		assert.Equal(t, chat.FinishReasonToolCalls, fr)
		// Sanity-check that the JSON we accumulated is parseable.
		var v map[string]any
		require.NoError(t, json.Unmarshal([]byte(calls[0].Function.Arguments), &v))
	})

	t.Run("parallel tool calls collected by ID", func(t *testing.T) {
		t.Parallel()
		s := &fakeStream{responses: []chat.MessageStreamResponse{
			{Choices: []chat.MessageStreamChoice{{Delta: chat.MessageDelta{ToolCalls: []tools.ToolCall{
				{ID: "a", Function: tools.FunctionCall{Name: "fn1", Arguments: `{}`}},
				{ID: "b", Function: tools.FunctionCall{Name: "fn2", Arguments: `{}`}},
			}}, FinishReason: chat.FinishReasonToolCalls}}},
		}}
		_, calls, fr, err := drainSamplingStreamWithTools(s)
		require.NoError(t, err)
		require.Len(t, calls, 2)
		assert.Equal(t, "a", calls[0].ID)
		assert.Equal(t, "b", calls[1].ID)
		assert.Equal(t, chat.FinishReasonToolCalls, fr)
	})

	t.Run("inferred tool_calls when provider omits finish reason", func(t *testing.T) {
		t.Parallel()
		s := &fakeStream{responses: []chat.MessageStreamResponse{
			{Choices: []chat.MessageStreamChoice{{Delta: chat.MessageDelta{ToolCalls: []tools.ToolCall{
				{ID: "x", Function: tools.FunctionCall{Name: "fn", Arguments: `{}`}},
			}}}}},
		}}
		_, calls, fr, err := drainSamplingStreamWithTools(s)
		require.NoError(t, err)
		require.Len(t, calls, 1)
		assert.Equal(t, chat.FinishReasonToolCalls, fr)
	})

	t.Run("stop reconciled to tool_calls when calls present", func(t *testing.T) {
		t.Parallel()
		// Provider says "stop" but also emits tool calls — reconciliation
		// should treat this as a tool-call turn (the early-exit on stop fires
		// in handleStream-style aggregation, then reconciliation upgrades).
		s := &fakeStream{responses: []chat.MessageStreamResponse{
			{Choices: []chat.MessageStreamChoice{{Delta: chat.MessageDelta{ToolCalls: []tools.ToolCall{
				{ID: "x", Function: tools.FunctionCall{Name: "fn", Arguments: `{}`}},
			}}, FinishReason: chat.FinishReasonStop}}},
		}}
		_, _, fr, err := drainSamplingStreamWithTools(s)
		require.NoError(t, err)
		assert.Equal(t, chat.FinishReasonToolCalls, fr)
	})

	t.Run("tool-call-only chunks continue without falling into finish logic", func(t *testing.T) {
		t.Parallel()
		// Reproduces the streaming.go-style short-circuit: any chunk that
		// carries only tool-call deltas (no finish reason, no content) must
		// continue to the next chunk rather than fall through downstream
		// finish-reason processing. The argument fragments arrive across
		// three intermediate chunks before the terminal one.
		s := &fakeStream{responses: []chat.MessageStreamResponse{
			{Choices: []chat.MessageStreamChoice{{Delta: chat.MessageDelta{ToolCalls: []tools.ToolCall{
				{ID: "c1", Type: "function", Function: tools.FunctionCall{Name: "fn"}},
			}}}}},
			{Choices: []chat.MessageStreamChoice{{Delta: chat.MessageDelta{ToolCalls: []tools.ToolCall{
				{ID: "c1", Function: tools.FunctionCall{Arguments: `{"a":`}},
			}}}}},
			{Choices: []chat.MessageStreamChoice{{Delta: chat.MessageDelta{ToolCalls: []tools.ToolCall{
				{ID: "c1", Function: tools.FunctionCall{Arguments: `1,"b":`}},
			}}}}},
			{Choices: []chat.MessageStreamChoice{{Delta: chat.MessageDelta{ToolCalls: []tools.ToolCall{
				{ID: "c1", Function: tools.FunctionCall{Arguments: `2}`}},
			}}, FinishReason: chat.FinishReasonToolCalls}}},
		}}
		_, calls, fr, err := drainSamplingStreamWithTools(s)
		require.NoError(t, err)
		require.Len(t, calls, 1)
		assert.Equal(t, `{"a":1,"b":2}`, calls[0].Function.Arguments)
		assert.Equal(t, chat.FinishReasonToolCalls, fr)
	})
}
