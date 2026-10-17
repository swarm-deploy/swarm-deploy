package alertmanagement

import (
	"errors"
	"fmt"
	"net"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/codec"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/faults"
	"github.com/swarm-deploy/swarm-deploy/internal/testutil"
)

func TestSanitizedDockerErrorsKeepAlertThresholdAfterDecoding(t *testing.T) {
	for _, temporary := range []bool{true, false} {
		t.Run(fmt.Sprintf("temporary=%t", temporary), func(t *testing.T) {
			store := modelstore.NewSQLStore(testutil.OpenSQLite(t))
			subscriber := newTestSubscriber(store)
			var cause error = errors.New("private-request-marker AGE-SECRET-KEY-private-marker")
			if temporary {
				cause = &net.OpError{Op: "private-request-marker", Err: syscall.ETIMEDOUT}
			}
			for attempt := 1; attempt <= 3; attempt++ {
				event := &events.DeployFailed{
					DeployEvent: events.DeployEvent{StackName: "api"},
					Error:       fmt.Errorf("create secret api-password: %w", faults.SanitizeError(cause, "Docker API request failed")),
				}
				payload, err := codec.Encode(event)
				require.NoError(t, err)
				assert.NotContains(t, string(payload), "private-")
				decoded, err := codec.Decode(event.Type().Name(), codec.Version, payload)
				require.NoError(t, err)
				require.NoError(t, subscriber.Handle(t.Context(), events.Envelope{ID: fmt.Sprint(attempt), Event: decoded}))
				alerts, err := store.List(t.Context(), modelstore.ListFilter{})
				require.NoError(t, err)
				if temporary && attempt < 3 {
					assert.Empty(t, alerts, "temporary API failures must retain the existing threshold")
					continue
				}
				require.Len(t, alerts, 1)
				assert.EqualValues(t, attempt, alerts[0].Occurrences)
				assert.Contains(t, alerts[0].Message, "Docker API request failed")
				assert.NotContains(t, alerts[0].Message, "private-")
			}
		})
	}
}
