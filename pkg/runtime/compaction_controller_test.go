package runtime

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/docker/docker-agent/pkg/agent"
	"github.com/docker/docker-agent/pkg/chat"
	"github.com/docker/docker-agent/pkg/session"
	"github.com/docker/docker-agent/pkg/team"
	"github.com/docker/docker-agent/pkg/tools"
	"github.com/docker/docker-agent/pkg/userconfig"
)

func TestCompactionControllerWaitsAtBlockingThreshold(t *testing.T) {
	t.Parallel()
	entered, release := make(chan struct{}), make(chan struct{})
	prov := &mockCompactor{id: "anthropic/claude-test", opts: map[string]any{nativeCompactionOpt: true}, result: nativeResult(), onCall: func() { close(entered); <-release }}
	root := agent.New("root", "help", agent.WithModel(prov))
	rt, err := NewLocalRuntime(t.Context(), team.New(team.WithAgents(root)), WithModelStore(mockModelStoreWithLimit{limit: 100_000}))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, rt.Close()) })
	sess := nativeTestSession()
	sess.SetUsage(75_000, 0)
	c := newCompactionController(rt, sess, EventSinkFunc(func(Event) {}), &userconfig.Settings{BackgroundCompaction: true})
	defer c.Close(t.Context())
	c.BeforeTurn(t.Context(), root, 100_000)
	require.NotNil(t, c.job)
	<-entered
	sess.SetUsage(95_000, 0)
	before := sess.OwnMessageCount()
	sess.AddMessage(session.UserMessage("new tail"))
	go func() { close(release) }()
	c.AfterMessages(t.Context(), root, 100_000, before)
	assert.Nil(t, c.job)
	assert.Equal(t, "native summary", sess.LastSummary())
	assert.Equal(t, 1, prov.callCount(), "reuse the background summary rather than compacting twice")
}

func TestCompactionControllerCloseCancelsAndJoins(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	job := &backgroundCompaction{cancel: cancel, done: done}
	go func() { <-ctx.Done(); close(done) }()
	c := &compactionController{job: job, events: EventSinkFunc(func(Event) {})}
	c.Close(t.Context())
	assert.Nil(t, c.job)
	select {
	case <-done:
	default:
		t.Fatal("worker must finish before Close returns")
	}
	c.Close(t.Context())
}

func TestCompactionControllerSkipsDisabledAndPendingTools(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"disabled setting", "disabled agent", "disabled runtime", "pending tools"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			prov := &mockCompactor{id: "anthropic/claude-test", opts: map[string]any{nativeCompactionOpt: true}, result: nativeResult()}
			root := agent.New("root", "help", agent.WithModel(prov), agent.WithSessionCompaction(mode != "disabled agent"))
			rt, err := NewLocalRuntime(t.Context(), team.New(team.WithAgents(root)), WithSessionCompaction(mode != "disabled runtime"), WithModelStore(mockModelStoreWithLimit{limit: 100_000}))
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, rt.Close()) })
			sess := nativeTestSession()
			sess.SetUsage(75_000, 0)
			if mode == "pending tools" {
				sess.AddMessage(session.NewAgentMessage("root", &chat.Message{Role: chat.MessageRoleAssistant, ToolCalls: []tools.ToolCall{{ID: "pending", Function: tools.FunctionCall{Name: "test"}}}}))
			}
			c := newCompactionController(rt, sess, EventSinkFunc(func(Event) {}), &userconfig.Settings{BackgroundCompaction: mode != "disabled setting"})
			defer c.Close(t.Context())
			c.BeforeTurn(t.Context(), root, 100_000)
			assert.Nil(t, c.job)
			assert.Zero(t, prov.callCount())
		})
	}
}
