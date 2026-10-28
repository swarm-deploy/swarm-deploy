package deployer

import (
	"errors"
	"fmt"
	"net"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/faults"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	"go.uber.org/mock/gomock"
)

func TestReconcileSecretLabelsBeforeExternalRendering(t *testing.T) {
	for _, scenario := range []string{"changed", "removed", "unchanged", "external", "update error"} {
		t.Run(scenario, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			secrets := swarm.NewMockSecretManager(ctrl)
			labels := map[string]string{"com.docker.stack.namespace": "app", "purpose": "old"}
			object := &compose.Secret{Name: "existing", Labels: *compose.NewLabels(map[string]string{"purpose": "new"})}
			if scenario == "removed" {
				object.Labels = compose.Labels{}
			}
			if scenario == "unchanged" {
				labels["purpose"] = "new"
			}
			if scenario == "external" {
				object.External = true
			}
			secrets.EXPECT().List(gomock.Any(), gomock.Any()).Return([]swarm.Secret{{ID: "id", Name: "existing", Labels: labels}}, nil)
			if scenario == "changed" || scenario == "removed" || scenario == "update error" {
				var updateErr error
				if scenario == "update error" {
					updateErr = errors.New("private-marker")
				}
				secrets.EXPECT().UpdateLabels(gomock.Any(), "id", resourceLabels("app", object.Labels.Map)).Return(updateErr)
			}
			r := newResourceReconciler(&swarm.Swarm{Secrets: secrets})
			resolved, err := r.Reconcile(t.Context(), "app", nil, compose.Secrets{"password": object}, nil)
			if scenario == "update error" {
				require.Error(t, err)
				assert.NotContains(t, err.Error(), "private-marker")
				return
			}
			require.NoError(t, err)
			assert.Equal(t, ResolvedResource{ID: "id", Name: "existing"}, resolved.secrets["password"])
		})
	}
}

func TestReconcileSecretOwnershipConflicts(t *testing.T) {
	for _, test := range []struct {
		name     string
		owner    string
		external bool
	}{
		{name: "another stack", owner: "other-stack"},
		{name: "no ownership label"},
		{name: "external owned by another stack", owner: "other-stack", external: true},
		{name: "external without ownership label", external: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			secrets := swarm.NewMockSecretManager(gomock.NewController(t))
			secrets.EXPECT().List(gomock.Any(), gomock.Any()).Return([]swarm.Secret{
				{ID: "owned", Name: "a-owned", Labels: map[string]string{"com.docker.stack.namespace": "app", "purpose": "old"}},
				{ID: "conflict", Name: "z-shared", Labels: map[string]string{"com.docker.stack.namespace": test.owner}},
			}, nil)
			objects := compose.Secrets{"shared": {Name: "z-shared", External: test.external}}
			if !test.external {
				// Even the earlier owned resource must not be updated if a later one conflicts.
				objects["a"] = &compose.Secret{Name: "a-owned"}
			}
			r := newResourceReconciler(&swarm.Swarm{Secrets: secrets})
			resolved, err := r.Reconcile(t.Context(), "app", nil, objects, nil)
			if !test.external {
				require.ErrorContains(t, err, "ownership conflict")
				assert.Nil(t, resolved.secrets)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, ResolvedResource{ID: "conflict", Name: "z-shared"}, resolved.secrets["shared"])
			// No Create, UpdateLabels or Remove calls are allowed in any scenario.
		})
	}
}

func TestSecretAPIFailuresPreserveTemporaryClassification(t *testing.T) {
	for _, operation := range []string{"create", "update"} {
		for _, temporary := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/temporary=%t", operation, temporary), func(t *testing.T) {
				secrets := swarm.NewMockSecretManager(gomock.NewController(t))
				var cause error = errors.New("private-request-marker AGE-SECRET-KEY-private-marker")
				if temporary {
					cause = &net.OpError{Op: "private-request-marker", Net: "tcp", Err: syscall.ETIMEDOUT}
				}
				apiErr := fmt.Errorf("private-request-data: %w", cause)
				object := &compose.Secret{Name: "app-password"}
				if operation == "create" {
					secrets.EXPECT().List(gomock.Any(), gomock.Any()).Return(nil, nil)
					secrets.EXPECT().Create(gomock.Any(), gomock.Any()).Return("", apiErr)
				} else {
					secrets.EXPECT().List(gomock.Any(), gomock.Any()).Return([]swarm.Secret{{
						ID: "existing", Name: object.Name, Labels: map[string]string{"com.docker.stack.namespace": "app", "old": "label"},
					}}, nil)
					secrets.EXPECT().UpdateLabels(gomock.Any(), "existing", resourceLabels("app", nil)).Return(apiErr)
				}
				r := newResourceReconciler(&swarm.Swarm{Secrets: secrets})
				_, err := r.Reconcile(t.Context(), "app", nil, compose.Secrets{"password": object}, map[string][]byte{"password": []byte("private-payload-marker")})
				require.ErrorContains(t, err, operation+" secret app-password: Docker API request failed")
				var classified faults.Error
				require.ErrorAs(t, err, &classified)
				assert.Equal(t, temporary, classified.Temporary())
				assert.NotContains(t, fmt.Sprintf("%+v %#v", err, err), "private-")
				assert.NotErrorIs(t, err, apiErr, "sensitive cause must not remain in the error chain")
			})
		}
	}
}
