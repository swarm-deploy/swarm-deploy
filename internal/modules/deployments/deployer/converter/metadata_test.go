package converter

import (
	"context"
	"errors"
	"fmt"
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/faults"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	"go.uber.org/mock/gomock"
)

func TestMetadataFailuresPreserveRetryClassificationWithoutSensitiveCause(t *testing.T) {
	for _, resource := range []string{"network", "config", "secret"} {
		for _, failure := range []struct {
			name      string
			cause     error
			temporary bool
		}{
			{"temporary", &net.DNSError{Err: "private-value", IsTemporary: true}, true},
			{"deadline", fmt.Errorf("private-value: %w", context.DeadlineExceeded), true},
			{"permanent", errors.New("private-value"), false},
		} {
			t.Run(resource+"/"+failure.name, func(t *testing.T) {
				converter, networks, configs, secrets := newTestConverter(t)
				desired := parseStack(t, "services: {app: {image: alpine}}")
				if resource == "network" {
					networks.EXPECT().Get(gomock.Any(), "demo_default").Return(swarm.Network{}, failure.cause)
				} else {
					networks.EXPECT().Get(gomock.Any(), "demo_default").Return(existingNetwork("demo_default"), nil)
				}
				switch resource {
				case "config":
					desired.Configs = compose.Configs{"app": &compose.Config{External: true}}
					configs.EXPECT().List(gomock.Any(), gomock.Any()).Return(nil, failure.cause)
				case "secret":
					desired.Secrets = compose.Secrets{"app": &compose.Secret{External: true}}
					secrets.EXPECT().List(gomock.Any(), gomock.Any()).Return(nil, failure.cause)
				}
				_, err := converter.Convert(t.Context(), "demo", desired)
				require.Error(t, err)
				var classified faults.Error
				require.ErrorAs(t, err, &classified)
				assert.Equal(t, failure.temporary, classified.Temporary())
				assert.Nil(t, errors.Unwrap(err))
				assert.NotContains(t, fmt.Sprintf("%v %+v %#v", err, err, err), "private-value")
			})
		}
	}
}
