package deployer

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
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
