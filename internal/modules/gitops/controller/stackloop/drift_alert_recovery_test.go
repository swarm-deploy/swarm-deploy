package stackloop

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement"
	alertmodel "github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement/model"
	alertstore "github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/dispatcher"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/controller/stackloop/drift"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/model"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	"go.uber.org/mock/gomock"
)

func TestDriftAlertRecoveryAfterManualReplicaScale(t *testing.T) {
	tests := []struct {
		name           string
		actualReplicas uint64
		retryDeferred  bool
		protected      bool
		wantFixed      bool
		wantStatus     alertmodel.AlertStatus
	}{
		{
			name: "regular reconciliation confirms manual recovery",
			actualReplicas: 3, wantFixed: true, wantStatus: alertmodel.AlertStatusResolved,
		},
		{
			name: "deferred retry must not hide confirmed manual recovery",
			actualReplicas: 3, retryDeferred: true, protected: true,
			wantFixed: true, wantStatus: alertmodel.AlertStatusResolved,
		},
		{
			name: "deferred retry retains alert while replica drift remains",
			actualReplicas: 2, retryDeferred: true, protected: true,
			wantStatus: alertmodel.AlertStatusOpen,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := t.Context()
			db, _ := newSQLState(t)
			alerts := alertstore.NewSQLStore(db)
			subscriber := alertmanagement.NewSQLSubscriber(
				alerts, &dispatcher.NopDispatcher{},
				func(alertmodel.AlertKind) []events.AlertRecipient { return nil }, &config.Config{},
			)
			at := time.Now().UTC()
			require.NoError(t, subscriber.Handle(ctx, events.Envelope{
				ID: "drift-detected", OccurredAt: at,
				Event: &events.ServiceDrifted{
					StackName: "app", ServiceName: "api",
					Reason: "replica count differs: desired=3 actual=1",
				},
			}))

			// The manual API operation reports a mutation, not verified convergence.
			require.NoError(t, subscriber.Handle(ctx, events.Envelope{
				ID: "replicas-changed", OccurredAt: at.Add(time.Second),
				Event: &events.ServiceReplicasIncreased{
					StackName: "app", ServiceName: "api",
					PreviousReplicas: 1, CurrentReplicas: tt.actualReplicas,
				},
			}))

			desiredReplicas := uint64(3)
			desired := &compose.File{Compose: compose.Compose{Services: compose.Services{{
				Name: "api", Deploy: compose.ServiceDeploy{Replicas: &desiredReplicas},
			}}}}
			verified, err := drift.NewAnalyzer().Analyze(drift.AnalyzeRequest{
				Stack: config.StackSpec{Name: "app"},
				Desired: *desired,
				Live: []swarm.StackService{{Name: "api", Replicas: &tt.actualReplicas}},
			})
			require.NoError(t, err)

			publisher := dispatcher.NewMockDispatcher(gomock.NewController(t))
			fixedEvents := 0
			publisher.EXPECT().Publish(gomock.Any(), gomock.Any()).DoAndReturn(
				func(ctx context.Context, event events.Event) error {
					if _, ok := event.(*events.ServiceDriftFixed); ok {
						fixedEvents++
					}
					return subscriber.Handle(ctx, events.Envelope{
						ID: "verified-reconciliation", OccurredAt: at.Add(2 * time.Second), Event: event,
					})
				},
			).AnyTimes()

			payload := &pipelinePayload{
				Desired: desired, RetryDeferred: tt.retryDeferred,
				Drift: verified.Drifts, RemainingDrift: verified.Drifts,
			}
			if tt.protected {
				payload.ProtectedServices = []string{"api"}
			}
			reconciler := &Reconciler{event: publisher}
			previous := model.Stack{Services: map[string]model.Service{
				"api": {SyncStatus: model.SyncStatusOutOfSync},
			}}
			require.NoError(t, reconciler.publishDriftEvents(ctx, ReconciliationRequest{
				Stack: config.StackSpec{Name: "app"}, Commit: "current",
			}, previous, payload))

			if tt.wantFixed {
				assert.Equal(t, 1, fixedEvents, "confirmed convergence must publish ServiceDriftFixed")
			} else {
				assert.Zero(t, fixedEvents, "unresolved drift must not publish ServiceDriftFixed")
			}
			stored, err := alerts.List(ctx, alertstore.ListFilter{Kind: alertmodel.AlertKindServiceDrift})
			require.NoError(t, err)
			require.Len(t, stored, 1)
			assert.Equal(t, tt.wantStatus, stored[0].Status)
		})
	}
}
