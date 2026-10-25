package lifecycle_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"gotest.tools/v3/assert"

	"github.com/docker/docker-agent/pkg/tools/lifecycle"
)

func TestSupervisor_StartAfterStop(t *testing.T) {
	t.Parallel()
	for _, stopFirst := range []bool{false, true} {
		t.Run(map[bool]string{false: "started", true: "never-started"}[stopFirst], func(t *testing.T) {
			sessions := []*fakeSession{newFakeSession(), newFakeSession(), newFakeSession()}
			c := newScriptedConnector(scriptStep{session: sessions[0]}, scriptStep{session: sessions[1]}, scriptStep{session: sessions[2]})
			s := lifecycle.New("test", c, lifecycle.Policy{})
			if stopFirst {
				assert.NilError(t, s.Stop(t.Context()))
			}
			for _, sess := range sessions {
				assert.NilError(t, s.Start(t.Context()))
				assert.Equal(t, s.State().State, lifecycle.StateReady)
				sess.waitParked(t)
				assert.NilError(t, s.Stop(t.Context()))
				assert.Equal(t, s.State().State, lifecycle.StateStopped)
				sess.waitClosed(t)
			}
			assert.Equal(t, c.Calls(), len(sessions))
		})
	}
}

func TestSupervisor_StartWaitsForStoppedWatcher(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		disconnected, release := make(chan struct{}), make(chan struct{})
		first, second := newFakeSession(), newFakeSession()
		c := newScriptedConnector(scriptStep{session: first}, scriptStep{session: second})
		s := lifecycle.New("test", c, lifecycle.Policy{OnDisconnect: func(error) { close(disconnected); <-release }})
		assert.NilError(t, s.Start(t.Context()))
		first.fail(errors.New("disconnected"))
		<-disconnected
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		assert.ErrorIs(t, s.Stop(ctx), context.Canceled)
		assert.ErrorIs(t, s.Start(ctx), context.Canceled)
		assert.Equal(t, c.Calls(), 1)
		resumed := make(chan error, 1)
		go func() { resumed <- s.Start(t.Context()) }()
		synctest.Wait()
		assert.Equal(t, c.Calls(), 1)
		close(release)
		assert.NilError(t, <-resumed)
		assert.Equal(t, c.Calls(), 2)
		assert.NilError(t, s.Stop(t.Context()))
	})
}

type resumeConnector struct {
	calls         atomic.Int32
	release       chan struct{}
	first, second *fakeSession
}

func (c *resumeConnector) Connect(context.Context) (lifecycle.Session, error) {
	if c.calls.Add(1) == 1 {
		<-c.release
		return c.first, nil
	}
	return c.second, nil
}

func TestSupervisor_StartAfterStopWaitsForLateConnect(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		c := &resumeConnector{release: make(chan struct{}), first: newFakeSession(), second: newFakeSession()}
		s := lifecycle.New("test", c, lifecycle.Policy{StartupTimeout: time.Millisecond})
		assert.ErrorIs(t, s.Start(t.Context()), lifecycle.ErrInitTimeout)
		assert.NilError(t, s.Stop(t.Context()))
		resumed := make(chan error, 1)
		go func() { resumed <- s.Start(t.Context()) }()
		synctest.Wait()
		assert.Equal(t, c.calls.Load(), int32(1))
		close(c.release)
		assert.NilError(t, <-resumed)
		c.first.waitClosed(t)
		assert.Equal(t, c.calls.Load(), int32(2))
		assert.NilError(t, s.Stop(t.Context()))
	})
}
