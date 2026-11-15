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

func TestSyncPauseResponsesValidateAndDoNotDoubleCount(t *testing.T) {
	state := modelstore.NewMemoryStore()
	require.NoError(t, state.Update(t.Context(), func(runtime *gitopsmodel.Runtime) {
		runtime.Stacks["app"] = gitopsmodel.Stack{Services: map[string]gitopsmodel.Service{"api": {}, "worker": {}}}
	}))
	pauses := syncpause.NewService(testutil.OpenSQLite(t), state, pauseStackProvider{}, &dispatcher.NopDispatcher{})
	_, err := pauses.SetStack(t.Context(), "app", nil)
	require.NoError(t, err)
	_, err = pauses.SetService(t.Context(), "app", "api", nil)
	require.NoError(t, err)
	h := &handler{syncPauses: pauses}

	stackResponse, err := h.GetStackSyncPauses(t.Context(), generated.GetStackSyncPausesParams{Stack: "app"})
	require.NoError(t, err)
	stack := stackResponse.(*generated.StackSyncPausesResponse)
	require.NoError(t, stack.Validate())
	assert.Equal(t, 2, stack.EffectivelyPausedServices)
	require.Len(t, stack.EffectiveServices, 2)
	assert.Equal(t, generated.EffectiveSyncPauseSourceBoth, stack.EffectiveServices[0].Source)
	assert.Equal(t, generated.EffectiveSyncPauseSourceStack, stack.EffectiveServices[1].Source)
	require.Len(t, stack.ServicePauses, 1)
	assert.True(t, stack.ServicePauses[0].ExpiresAt.IsNull())

	service, err := h.GetServiceSyncPause(t.Context(), generated.GetServiceSyncPauseParams{Stack: "app", Service: "api"})
	require.NoError(t, err)
	require.NoError(t, service.Validate())
	assert.True(t, service.EffectivePaused)
	source, ok := service.EffectiveSource.Get()
	require.True(t, ok)
	assert.Equal(t, generated.EffectiveSyncPauseSourceBoth, source)
}
