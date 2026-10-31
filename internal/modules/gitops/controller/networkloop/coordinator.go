package networkloop

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/configstore"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/dispatcher"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/model"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/tracing"
	"github.com/swarm-deploy/swarm-deploy/internal/storage"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// Coordinator coordinates network reconciliation and transactional result persistence.
type Coordinator struct {
	db          storage.Database
	configStore *configstore.ConfigStore
	stateStore  modelstore.Store
	reconciler  *Reconciler
	event       dispatcher.Dispatcher
	tracer      trace.Tracer
}

// NewCoordinator creates a coordinator for configured networks, state persistence, and events.
func NewCoordinator(
	db storage.Database,
	configStore *configstore.ConfigStore,
	stateStore modelstore.Store,
	reconciler *Reconciler,
	eventDispatcher dispatcher.Dispatcher,
) *Coordinator {
	return &Coordinator{db: db, configStore: configStore, stateStore: stateStore,
		reconciler: reconciler, event: eventDispatcher,
		tracer: otel.Tracer("github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/controller"),
	}
}

// Reconcile applies all configured networks and persists their runtime projection.
func (r *Coordinator) Reconcile(ctx context.Context, commit string) error {
	ctx, span := r.tracer.Start(ctx, "controller.SyncNetworks", trace.WithAttributes(
		tracing.SyncCommitSha.String(commit)))
	defer span.End()

	networks := r.configStore.Networks()
	if len(networks) == 0 {
		return r.stateStore.Update(ctx, func(state *model.Runtime) { state.Networks = map[string]model.Network{} })
	}
	currentState, err := r.stateStore.Read(ctx)
	if err != nil {
		return err
	}

	syncedAt := time.Now()
	nextState := make(map[string]model.Network, len(networks))
	var reconcileErrs []error
	var createdEvents []*events.NetworkCreated
	for _, networkCfg := range networks {
		if previous, synced := syncedNetwork(currentState, networkCfg.Name, commit); synced {
			nextState[networkCfg.Name] = previous
			continue
		}
		networkState, created, reconcileErr := r.reconcileNetwork(ctx, networkCfg, commit, syncedAt)
		if created != nil {
			createdEvents = append(createdEvents, created)
		}
		if reconcileErr != nil {
			reconcileErrs = append(reconcileErrs, fmt.Errorf("network %s: %w", networkCfg.Name, reconcileErr))
		}
		nextState[networkCfg.Name] = networkState
	}

	persistErr := r.persist(ctx, nextState, createdEvents)
	joinedErr := errors.Join(append(reconcileErrs, persistErr)...)
	if joinedErr != nil {
		span.RecordError(joinedErr)
		span.SetStatus(codes.Error, joinedErr.Error())
	} else {
		span.SetStatus(codes.Ok, "")
	}
	return joinedErr
}

func syncedNetwork(state model.Runtime, name, commit string) (model.Network, bool) {
	previous, exists := state.Networks[name]
	return previous, exists && previous.LastCommit == commit &&
		(previous.LastStatus == "success" || previous.LastStatus == "no_change")
}

func (r *Coordinator) reconcileNetwork(
	ctx context.Context, networkCfg config.NetworkSpec, commit string, syncedAt time.Time,
) (model.Network, *events.NetworkCreated, error) {
	skipped, created, err := r.reconciler.Reconcile(ctx, networkCfg)
	state := model.Network{Driver: networkCfg.Driver, LastCommit: commit,
		LastStatus: "success", LastSyncAt: syncedAt}
	if err != nil {
		state.LastStatus, state.LastError = "failed", err.Error()
	} else if skipped {
		state.LastStatus = "no_change"
	}
	return state, created, err
}

func (r *Coordinator) persist(
	ctx context.Context, nextState map[string]model.Network, createdEvents []*events.NetworkCreated,
) error {
	return r.db.WithinTransaction(ctx, func(ctx context.Context) error {
		if err := r.stateStore.Update(ctx, func(state *model.Runtime) {
			state.Networks = nextState
		}); err != nil {
			return err
		}
		for _, event := range createdEvents {
			if err := r.event.Publish(ctx, event); err != nil {
				return err
			}
		}
		return nil
	})
}
