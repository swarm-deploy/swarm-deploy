package alertmanagement

import (
	"errors"
	"fmt"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement/model"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/codec"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/dispatcher"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/outbox"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/faults"
)

func TestDeploymentCompletionOutboxDeliversAllCauses(t *testing.T) {
	pull := faults.Cause{Code: faults.CodeImagePullDenied, Summary: "registry denied access"}
	mount := faults.Cause{Code: faults.CodeMountFailed, Summary: "mount failed token=private-value", Temporary: true}
	maskedMount := faults.Cause{Code: mount.Code, Summary: "mount failed token=****", Temporary: true}
	otherMount := faults.Cause{Code: mount.Code, Summary: "mount failed on another volume", Temporary: true}
	for _, tc := range []struct {
		name         string
		causes       []faults.Cause
		wantCauses   []faults.Cause
		openAfter    int
		wantFindings []faults.Cause
	}{
		{"permanent first", []faults.Cause{pull, mount}, []faults.Cause{pull, maskedMount}, 1, []faults.Cause{pull, maskedMount}},
		{"temporary first", []faults.Cause{mount, pull}, []faults.Cause{maskedMount, pull}, 1, []faults.Cause{pull, maskedMount}},
		{"two temporary summaries", []faults.Cause{mount, otherMount}, []faults.Cause{maskedMount, otherMount},
			3, []faults.Cause{maskedMount, otherMount}},
		{"same finding on two services", []faults.Cause{pull, pull}, []faults.Cause{pull, pull}, 1, []faults.Cause{pull}},
		{"single cause", []faults.Cause{pull}, []faults.Cause{pull}, 1, []faults.Cause{pull}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()
			db, repo := openAlertStore(t)
			bus := dispatcher.NewOutboxDispatcher(outbox.New(db))
			subscriber := newTestSQLSubscriber(repo)
			require.NoError(t, bus.Subscribe(events.TypeNameDeployFailed, "alerts", subscriber))
			service := deployments.NewService(db, bus)
			desired := compose.File{Compose: compose.Compose{Services: compose.Services{
				{Name: "api", Image: "api:2"}, {Name: "worker", Image: "worker:2"},
			}}}
			branches := []error{errors.New("service sidecar: unknown technical error")}
			for i, cause := range tc.causes {
				branches = append(branches, fmt.Errorf("service %s: %w", desired.Compose.Services[i].Name, testDeploymentFault{cause}))
			}
			diagnostic := fmt.Errorf("verify service convergence: %w", errors.Join(branches...))
			for number := 1; number <= tc.openAfter; number++ {
				attempt, err := service.StartIfChanged(ctx, "app", fmt.Sprint(number), desired, true,
					deployments.StartOptions{ManualRetry: true, TargetServices: desired.Compose.Services.Names()})
				require.NoError(t, err)
				require.NotNil(t, attempt)
				require.NoError(t, service.Complete(ctx, attempt.ID, deployments.Completion{
					Outcome: deployments.CompletionConvergenceFailed, Diagnostic: diagnostic,
				}))
				var payloads []string
				require.NoError(t, db.Select(ctx, &payloads, "SELECT payload FROM outbox_events WHERE event_type=?", events.TypeNameDeployFailed))
				require.Len(t, payloads, 1)
				assert.NotContains(t, payloads[0], "private-value")
				assert.NotContains(t, payloads[0], "fault_code")
				event, err := codec.Decode(events.TypeNameDeployFailed, codec.Version, []byte(payloads[0]))
				require.NoError(t, err)
				assert.Equal(t, tc.wantCauses, faults.Causes(event.(*events.DeployFailed).Error))

				processed, err := bus.ProcessNext(ctx)
				require.NoError(t, err)
				require.True(t, processed)
				alerts, err := repo.List(ctx, modelstore.ListFilter{})
				require.NoError(t, err)
				if number < tc.openAfter {
					assert.Empty(t, alerts)
					continue
				}
				require.Len(t, alerts, 1)
				alert := alerts[0]
				assert.Equal(t, model.AlertStatusOpen, alert.Status)
				assert.EqualValues(t, number, alert.Occurrences)
				assert.Contains(t, alert.Message, "service api:")
				assert.Contains(t, alert.Message, "service sidecar: unknown technical error")
				if len(tc.causes) == 2 {
					assert.Contains(t, alert.Message, "service worker:")
				}
				var findings []faults.Cause
				for _, finding := range alert.Findings {
					findings = append(findings, faults.Cause{Code: finding.Code, Summary: finding.Summary})
				}
				want := slices.Clone(tc.wantFindings)
				for i := range want {
					want[i].Temporary = false // Alert.Findings carries the code and summary.
				}
				assert.ElementsMatch(t, want, findings)
			}
		})
	}
}

type testDeploymentFault struct{ cause faults.Cause }

func (e testDeploymentFault) Error() string          { return e.cause.Summary }
func (e testDeploymentFault) FaultCode() faults.Code { return e.cause.Code }
func (e testDeploymentFault) FaultSummary() string   { return e.cause.Summary }
func (e testDeploymentFault) Temporary() bool        { return e.cause.Temporary }
