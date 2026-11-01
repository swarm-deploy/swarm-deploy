package converter

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

func TestIgnoredSwarmOptionsDoNotAffectServiceSpec(t *testing.T) {
	for _, option := range []string{
		"privileged: true", "privileged: false", "depends_on: [db]",
		"depends_on: {db: {condition: service_healthy, restart: true}}",
		"restart: always", "restart: on-failure", "restart: 'no'",
	} {
		t.Run(option, func(t *testing.T) {
			converter, networks, _, _ := newTestConverter(t)
			networks.EXPECT().Get(gomock.Any(), "demo_default").Return(existingNetwork("demo_default"), nil).Times(2)
			base := parseStack(t, "services: {app: {image: alpine, deploy: {restart_policy: {condition: none}}}}")
			wanted, err := converter.Convert(t.Context(), "demo", base)
			require.NoError(t, err)
			ignored := parseStack(t, "services: {app: {image: alpine, "+option+", deploy: {restart_policy: {condition: none}}}}")
			require.NoError(t, converter.Validate("demo", ignored))
			actual, err := converter.Convert(t.Context(), "demo", ignored)
			require.NoError(t, err)
			assert.Equal(t, wanted, actual, "ignored options must not alter Docker specs or restart policy")
		})
	}
}

func TestIgnoredOptionsWarningsAreStructuredAndNotRepeatedByConversion(t *testing.T) {
	var output bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	converter, networks, _, _ := newTestConverter(t)
	desired := parseStack(t, "services: {app: {image: alpine, privileged: false, depends_on: [db], restart: always}}")
	require.NoError(t, converter.Validate("demo", desired))
	converter.WarnIgnoredOptions(t.Context(), "demo", desired)
	_, err := converter.PlanNetworks("demo", desired)
	require.NoError(t, err)
	networks.EXPECT().Get(gomock.Any(), "demo_default").Return(existingNetwork("demo_default"), nil)
	_, err = converter.Convert(t.Context(), "demo", desired)
	require.NoError(t, err)
	assert.Equal(t, 3, strings.Count(output.String(), `"level":"WARN"`))
	for _, field := range []string{"privileged", "depends_on", "restart"} {
		assert.Equal(t, 1, strings.Count(output.String(), `"field":"services.app.`+field+`"`))
	}
}
