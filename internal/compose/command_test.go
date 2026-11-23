package compose

import (
	jsonv1 "encoding/json"
	"encoding/json/v2"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
)

func TestNewCommandMarshalYAMLAsSequence(t *testing.T) {
	t.Parallel()

	command := NewCommand([]string{"/app/api", "--port", "8080"})

	raw, err := yaml.Marshal(command)
	require.NoError(t, err)

	assert.Equal(t, "- /app/api\n- --port\n- \"8080\"\n", string(raw))
}

func TestCommandExec(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		command  Command
		expected []string
	}{
		{name: "scalar", command: Command{Args: []string{`sh -c 'echo "a b"'`}}, expected: []string{"sh", "-c", `echo "a b"`}},
		{name: "list", command: NewCommand([]string{"echo", "a b"}), expected: []string{"echo", "a b"}},
		{name: "empty list", command: NewCommand([]string{}), expected: nil},
		{name: "empty scalar", command: Command{Args: []string{""}}, expected: nil},
		{name: "absent", command: Command{}, expected: nil},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			original := Command{Args: slices.Clone(tc.command.Args), isList: tc.command.isList}

			exec, err := tc.command.Exec()
			require.NoError(t, err)

			assert.Equal(t, NewCommand(tc.expected), exec)
			assert.Equal(t, original, tc.command, "Exec must not modify its receiver")
			again, err := exec.Exec()
			require.NoError(t, err)
			assert.Equal(t, exec, again, "Exec must be idempotent")
		})
	}
}

func TestCommandExecRejectsInvalidQuotingWithoutLeakingCommand(t *testing.T) {
	t.Parallel()

	_, err := Command{Args: []string{"echo 'secret-token"}}.Exec()

	require.EqualError(t, err, "invalid quoting")
}

func TestServiceHealthCanonical(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		test     Command
		expected []string
	}{
		{name: "scalar", test: Command{Args: []string{"curl -f http://localhost/"}},
			expected: []string{HealthcheckShell, "curl -f http://localhost/"}},
		{name: "blank scalar", test: Command{Args: []string{""}}, expected: []string{HealthcheckShell, ""}},
		{name: "list", test: NewCommand([]string{"CMD", "true"}), expected: []string{"CMD", "true"}},
		{name: "empty list", test: NewCommand([]string{}), expected: nil},
		{name: "absent", test: Command{}, expected: nil},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			health := ServiceHealth{Test: tc.test, Interval: "5s"}
			original := ServiceHealth{Test: Command{Args: slices.Clone(tc.test.Args), isList: tc.test.isList}, Interval: "5s"}

			canonical := health.Canonical()

			assert.Equal(t, ServiceHealth{Test: NewCommand(tc.expected), Interval: "5s"}, canonical)
			assert.Equal(t, original, health, "Canonical must not modify its receiver")
			assert.Equal(t, canonical, canonical.Canonical(), "Canonical must be idempotent")
		})
	}
}

func TestCommandJSON(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		command Command
		payload string
	}{
		{name: "exec form", command: NewCommand([]string{"echo", "a b"}), payload: `{"Args":["echo","a b"]}`},
		{name: "empty exec form", command: NewCommand(nil), payload: `{"Args":null}`},
		{name: "scalar form", command: Command{Args: []string{"echo hi"}}, payload: `{"Args":["echo hi"],"IsList":false}`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			payload, err := json.Marshal(tc.command, jsonv1.DefaultOptionsV1())
			require.NoError(t, err)
			var restored Command
			require.NoError(t, json.Unmarshal(payload, &restored, jsonv1.DefaultOptionsV1()))

			assert.JSONEq(t, tc.payload, string(payload))
			assert.Equal(t, tc.command.Args, restored.Args)
			assert.Equal(t, tc.command.IsList() || len(tc.command.Args) == 0, restored.IsList())
		})
	}
}

func TestCommandDecodesLegacyFormFlag(t *testing.T) {
	t.Parallel()

	tests := []struct {
		payload string
		isList  bool
	}{
		{payload: `{"Args":["echo","hi"],"IsList":true}`, isList: true},
		{payload: `{"Args":["echo hi"],"IsList":false}`, isList: false},
	}

	for _, tc := range tests {
		t.Run(tc.payload, func(t *testing.T) {
			t.Parallel()
			var command Command

			require.NoError(t, json.Unmarshal([]byte(tc.payload), &command, jsonv1.DefaultOptionsV1()))

			assert.Equal(t, tc.isList, command.IsList())
		})
	}
}
