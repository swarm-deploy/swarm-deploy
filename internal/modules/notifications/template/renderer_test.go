package template

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type testPayload struct {
	Name       string     `json:"name"`
	StartedAt  time.Time  `json:"started_at"`
	ResolvedAt *time.Time `json:"resolved_at"`
	Nested     testNested `json:"nested"`
}

type testNested struct {
	Value string `json:"value"`
}

func TestValidate(t *testing.T) {
	now := time.Date(2026, time.October, 13, 10, 30, 0, 0, time.FixedZone("test", 3*60*60))
	payload := testPayload{Name: "deployment", StartedAt: now, Nested: testNested{Value: "ok"}}

	tests := []struct {
		name      string
		template  string
		wantError string
	}{
		{name: "valid fields", template: `{{ .Name }} {{ .Nested.Value }}`},
		{name: "condition and nil", template: `{{ if .ResolvedAt }}resolved{{ else }}open{{ end }}`},
		{name: "syntax error", template: `{{ if }}`, wantError: "parse template"},
		{name: "unknown field", template: `{{ .Missing }}`, wantError: `can't evaluate field Missing`},
		{name: "unknown nested field", template: `{{ .Nested.Missing }}`, wantError: `can't evaluate field Missing`},
		{name: "unknown function", template: `{{ unknown .Name }}`, wantError: `function "unknown" not defined`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := Validate(test.template, payload)
			if test.wantError == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), test.wantError)
		})
	}
}

func TestDate(t *testing.T) {
	local := time.Date(2026, time.October, 13, 12, 34, 56, 0, time.FixedZone("test", 3*60*60))
	utc := local.UTC()

	tests := []struct {
		name     string
		template string
		payload  testPayload
		want     string
	}{
		{name: "time", template: `{{ date "2006-01-02 15:04 MST" .StartedAt }}`, payload: testPayload{StartedAt: local}, want: "2026-10-13 09:34 UTC"},
		{name: "pointer", template: `{{ date "02.01.2006 15:04" .ResolvedAt }}`, payload: testPayload{ResolvedAt: &utc}, want: "13.10.2026 09:34"},
		{name: "nil pointer", template: `{{ date "2006" .ResolvedAt }}`, payload: testPayload{}, want: ""},
		{name: "different layout", template: `{{ date "Jan _2 15:04" .StartedAt }}`, payload: testPayload{StartedAt: local}, want: "Oct 13 09:34"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			renderer, err := New(test.template)
			require.NoError(t, err)
			actual, err := renderer.Render(test.payload)
			require.NoError(t, err)
			assert.Equal(t, test.want, actual)
		})
	}
}

func TestDateRejectsUnsupportedValue(t *testing.T) {
	err := Validate(`{{ date "2006" .Name }}`, testPayload{Name: "not-a-date"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "date expects time.Time or *time.Time")
}
