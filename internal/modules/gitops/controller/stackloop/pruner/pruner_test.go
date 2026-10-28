package pruner

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/dispatcher"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/labelsdict"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/tracing"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.uber.org/mock/gomock"
)

type removeExpectation struct {
	serviceID string
	err       error
}

func TestServicePrunerPrune(t *testing.T) {
	errRemoveFailed := errors.New("remove failed")

	tests := []struct {
		name           string
		syncCfg        config.SyncPolicySpec
		stackCfg       config.StackSpec
		desired        []compose.Service
		stackServices  []swarm.StackService
		removeCalls    []removeExpectation
		expectedPruned []string
	}{
		{
			name:    "removes managed orphan services",
			syncCfg: config.SyncPolicySpec{Prune: true},
			stackCfg: config.StackSpec{
				Name: "app",
			},
			desired: []compose.Service{{Name: "api"}},
			stackServices: []swarm.StackService{
				{
					ID:     "service-api-id",
					Name:   "api",
					Labels: map[string]string{labelsdict.ServiceManagedLabelKey: labelsdict.ServiceManagedLabelValue},
				},
				{
					ID:     "service-worker-id",
					Name:   "worker",
					Labels: map[string]string{labelsdict.ServiceManagedLabelKey: labelsdict.ServiceManagedLabelValue},
				},
				{
					ID:     "service-legacy-id",
					Name:   "legacy",
					Labels: map[string]string{},
				},
			},
			removeCalls:    []removeExpectation{{serviceID: "service-worker-id"}},
			expectedPruned: []string{"worker"},
		},
		{
			name:    "skips service when prune label disabled",
			syncCfg: config.SyncPolicySpec{Prune: true},
			stackCfg: config.StackSpec{
				Name: "app",
			},
			stackServices: []swarm.StackService{
				{
					ID:   "service-worker-id",
					Name: "worker",
					Labels: map[string]string{
						labelsdict.ServiceManagedLabelKey:         labelsdict.ServiceManagedLabelValue,
						labelsdict.ServiceSyncPolicyPruneLabelKey: "false",
					},
				},
			},
			expectedPruned: []string{},
		},
		{
			name:    "skips service when stack prune policy disabled",
			syncCfg: config.SyncPolicySpec{Prune: true},
			stackCfg: config.StackSpec{
				Name: "app",
				Sync: config.StackSyncSpec{
					Policy: config.StackSyncPolicySpec{
						Prune: boolRef(false),
					},
				},
			},
			stackServices: []swarm.StackService{
				{
					ID:     "service-worker-id",
					Name:   "worker",
					Labels: map[string]string{labelsdict.ServiceManagedLabelKey: labelsdict.ServiceManagedLabelValue},
				},
			},
			expectedPruned: []string{},
		},
		{
			name:    "removes service when prune label enabled",
			syncCfg: config.SyncPolicySpec{Prune: false},
			stackCfg: config.StackSpec{
				Name: "app",
			},
			stackServices: []swarm.StackService{
				{
					ID:   "service-worker-id",
					Name: "worker",
					Labels: map[string]string{
						labelsdict.ServiceManagedLabelKey:         labelsdict.ServiceManagedLabelValue,
						labelsdict.ServiceSyncPolicyPruneLabelKey: "true",
					},
				},
			},
			removeCalls:    []removeExpectation{{serviceID: "service-worker-id"}},
			expectedPruned: []string{"worker"},
		},
		{
			name:    "ignores service not found on remove",
			syncCfg: config.SyncPolicySpec{Prune: true},
			stackCfg: config.StackSpec{
				Name: "app",
			},
			stackServices: []swarm.StackService{
				{
					ID:     "service-worker-id",
					Name:   "worker",
					Labels: map[string]string{labelsdict.ServiceManagedLabelKey: labelsdict.ServiceManagedLabelValue},
				},
			},
			removeCalls:    []removeExpectation{{serviceID: "service-worker-id", err: swarm.ErrServiceNotFound}},
			expectedPruned: []string{},
		},
		{
			name:    "returns no pruned services when live state empty",
			syncCfg: config.SyncPolicySpec{Prune: true},
			stackCfg: config.StackSpec{
				Name: "app",
			},
			expectedPruned: []string{},
		},
		{
			name:    "continues when remove fails",
			syncCfg: config.SyncPolicySpec{Prune: true},
			stackCfg: config.StackSpec{
				Name: "app",
			},
			stackServices: []swarm.StackService{
				{
					ID:     "service-worker-id",
					Name:   "worker",
					Labels: map[string]string{labelsdict.ServiceManagedLabelKey: labelsdict.ServiceManagedLabelValue},
				},
			},
			removeCalls: []removeExpectation{
				{
					serviceID: "service-worker-id",
					err:       errRemoveFailed,
				},
			},
			expectedPruned: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			serviceManager := swarm.NewMockServiceManager(ctrl)
			eventDispatcher := dispatcher.NewMockDispatcher(ctrl)
			pruner := NewServicePruner(serviceManager, eventDispatcher, tt.syncCfg)

			for _, call := range tt.removeCalls {
				serviceManager.EXPECT().Remove(gomock.Any(), call.serviceID).Return(call.err)
			}
			for _, name := range tt.expectedPruned {
				eventDispatcher.EXPECT().Publish(gomock.Any(), &events.ServicePruned{
					StackName: tt.stackCfg.Name, ServiceName: name, Commit: "commit-1",
				}).Return(nil)
			}

			prunedServices, err := pruner.Prune(
				context.Background(),
				PruneServicesRequest{
					Stack:   tt.stackCfg,
					Commit:  "commit-1",
					Desired: tt.desired,
					Live:    tt.stackServices,
				},
			)

			require.NoError(t, err, "prune services")
			assert.Equal(t, tt.expectedPruned, prunedServices, "unexpected pruned services")
		})
	}
}

func TestServicePrunerTracing(t *testing.T) {
	errRemoveFailed := errors.New("remove failed")
	errPublishFailed := errors.New("publish failed")

	tests := []struct {
		name                 string
		service              swarm.StackService
		syncCfg              config.SyncPolicySpec
		removeErr            error
		publishErr           error
		expectRemove         bool
		expectPublish        bool
		expectedPruned       []string
		expectedChildEvents  []string
		expectedChildStatus  codes.Code
		expectedParentPruned bool
		expectedParentStatus codes.Code
	}{
		{
			name: "records successful prune",
			service: swarm.StackService{
				ID:     "service-worker-id",
				Name:   "worker",
				Labels: map[string]string{labelsdict.ServiceManagedLabelKey: labelsdict.ServiceManagedLabelValue},
			},
			syncCfg:              config.SyncPolicySpec{Prune: true},
			expectRemove:         true,
			expectPublish:        true,
			expectedPruned:       []string{"worker"},
			expectedChildEvents:  []string{"Service pruned"},
			expectedChildStatus:  codes.Unset,
			expectedParentPruned: true,
			expectedParentStatus: codes.Unset,
		},
		{
			name: "records unmanaged service skip",
			service: swarm.StackService{
				ID:   "service-worker-id",
				Name: "worker",
			},
			syncCfg:              config.SyncPolicySpec{Prune: true},
			expectedPruned:       []string{},
			expectedChildEvents:  []string{"Service skipped: service unmanaged"},
			expectedChildStatus:  codes.Unset,
			expectedParentStatus: codes.Unset,
		},
		{
			name: "records policy skip",
			service: swarm.StackService{
				ID:     "service-worker-id",
				Name:   "worker",
				Labels: map[string]string{labelsdict.ServiceManagedLabelKey: labelsdict.ServiceManagedLabelValue},
			},
			expectedPruned:       []string{},
			expectedChildEvents:  []string{"Service skipped by sync policy"},
			expectedChildStatus:  codes.Unset,
			expectedParentStatus: codes.Unset,
		},
		{
			name: "records remove failure",
			service: swarm.StackService{
				ID:     "service-worker-id",
				Name:   "worker",
				Labels: map[string]string{labelsdict.ServiceManagedLabelKey: labelsdict.ServiceManagedLabelValue},
			},
			syncCfg:              config.SyncPolicySpec{Prune: true},
			removeErr:            errRemoveFailed,
			expectRemove:         true,
			expectedPruned:       []string{},
			expectedChildEvents:  []string{"exception"},
			expectedChildStatus:  codes.Error,
			expectedParentStatus: codes.Error,
		},
		{
			name: "records event publication failure",
			service: swarm.StackService{
				ID:     "service-worker-id",
				Name:   "worker",
				Labels: map[string]string{labelsdict.ServiceManagedLabelKey: labelsdict.ServiceManagedLabelValue},
			},
			syncCfg:              config.SyncPolicySpec{Prune: true},
			publishErr:           errPublishFailed,
			expectRemove:         true,
			expectPublish:        true,
			expectedPruned:       []string{"worker"},
			expectedChildEvents:  []string{"Service pruned", "exception"},
			expectedChildStatus:  codes.Error,
			expectedParentPruned: true,
			expectedParentStatus: codes.Unset,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := tracetest.NewSpanRecorder()
			provider := trace.NewTracerProvider(
				trace.WithSpanProcessor(recorder),
				trace.WithSampler(trace.AlwaysSample()),
			)
			previousProvider := otel.GetTracerProvider()
			otel.SetTracerProvider(provider)
			t.Cleanup(func() {
				otel.SetTracerProvider(previousProvider)
				require.NoError(t, provider.Shutdown(context.Background()))
			})

			ctrl := gomock.NewController(t)
			serviceManager := swarm.NewMockServiceManager(ctrl)
			eventDispatcher := dispatcher.NewMockDispatcher(ctrl)
			if tt.expectRemove {
				serviceManager.EXPECT().Remove(gomock.Any(), tt.service.ID).Return(tt.removeErr)
			}
			if tt.expectPublish {
				eventDispatcher.EXPECT().Publish(gomock.Any(), &events.ServicePruned{
					StackName: "app", ServiceName: tt.service.Name, Commit: "commit-1",
				}).Return(tt.publishErr)
			}

			pruner := NewServicePruner(serviceManager, eventDispatcher, tt.syncCfg)
			ctx, parent := provider.Tracer("test").Start(t.Context(), "parent")
			prunedServices, err := pruner.Prune(ctx, PruneServicesRequest{
				Stack:  config.StackSpec{Name: "app"},
				Commit: "commit-1",
				Live:   []swarm.StackService{tt.service},
			})
			parent.End()

			require.NoError(t, err, "prune services")
			assert.Equal(t, tt.expectedPruned, prunedServices, "unexpected pruned services")

			spans := recorder.Ended()
			require.Len(t, spans, 2, "parent and service spans must be ended")
			var parentSpan, childSpan trace.ReadOnlySpan
			for _, span := range spans {
				switch span.Name() {
				case "parent":
					parentSpan = span
				case "pruner.PruneService":
					childSpan = span
				}
			}
			require.NotNil(t, parentSpan, "parent span")
			require.NotNil(t, childSpan, "service span")
			assert.Equal(t, parentSpan.SpanContext().SpanID(), childSpan.Parent().SpanID(), "service span parent")
			assert.Equal(t, tt.expectedChildStatus, childSpan.Status().Code, "service span status")
			assert.Equal(t, tt.expectedParentStatus, parentSpan.Status().Code, "parent span status")

			serviceName := ""
			for _, attr := range childSpan.Attributes() {
				if attr.Key == tracing.ResourceServiceName {
					serviceName = attr.Value.AsString()
				}
			}
			assert.Equal(t, tt.service.Name, serviceName, "service span attribute")

			childEvents := make([]string, 0, len(childSpan.Events()))
			for _, event := range childSpan.Events() {
				childEvents = append(childEvents, event.Name)
			}
			for _, expectedEvent := range tt.expectedChildEvents {
				assert.Contains(t, childEvents, expectedEvent, "service span events")
			}

			parentPruned := false
			for _, event := range parentSpan.Events() {
				if event.Name == "Service pruned" {
					parentPruned = true
				}
			}
			assert.Equal(t, tt.expectedParentPruned, parentPruned, "parent prune event")
		})
	}
}

func boolRef(v bool) *bool {
	return &v
}
