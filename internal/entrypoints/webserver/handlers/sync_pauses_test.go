package handlers

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	generated "github.com/swarm-deploy/swarm-deploy/internal/entrypoints/webserver/generated"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/dispatcher"
	gitopsmodel "github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/model"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/syncpause"
	"github.com/swarm-deploy/swarm-deploy/internal/testutil"
)

type pauseStackProvider struct{}

func (pauseStackProvider) Stacks() []config.StackSpec { return []config.StackSpec{{Name: "app"}} }

func TestSyncPauseResponseValidatesAndCountsManagedServices(t *testing.T) {
	state := modelstore.NewMemoryStore()
	require.NoError(t, state.Update(t.Context(), func(runtime *gitopsmodel.Runtime) {
		runtime.Stacks["app"] = gitopsmodel.Stack{Services: map[string]gitopsmodel.Service{"api": {}, "worker": {}}}
	}))
	pauses := syncpause.NewService(testutil.OpenSQLite(t), state, pauseStackProvider{}, &dispatcher.NopDispatcher{})
	_, err := pauses.SetStack(t.Context(), "app", nil)
	require.NoError(t, err)
	h := &handler{syncPauses: pauses}

	stackResponse, err := h.GetStackSyncPauses(t.Context(), generated.GetStackSyncPausesParams{Stack: "app"})
	require.NoError(t, err)
	stack := stackResponse.(*generated.StackSyncPausesResponse)
	require.NoError(t, stack.Validate())
	assert.Equal(t, 2, stack.EffectivelyPausedServices)
	pause, ok := stack.StackPause.Get()
	require.True(t, ok)
	assert.Equal(t, "app", pause.Stack)
	assert.True(t, pause.ExpiresAt.IsNull())
}
