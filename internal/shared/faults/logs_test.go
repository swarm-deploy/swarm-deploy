package faults

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDiagnosticLogLines(t *testing.T) {
	for _, tc := range []struct {
		name string
		logs []string
		want []string
	}{
		{name: "slog error", logs: []string{`ERROR failed to load config err="unknown configuration field"`},
			want: []string{`ERROR failed to load config err="unknown configuration field"`}},
		{name: "configuration fields", logs: []string{`level=ERROR msg="failed to load config" err="unknown configuration field" file=/etc/app.yaml`},
			want: []string{`level=ERROR msg="failed to load config" err="unknown configuration field" file=/etc/app.yaml`}},
		{name: "safe equals", logs: []string{"application starting version=2 replicas=3", "expected=ready actual=failed"},
			want: []string{"application starting version=2 replicas=3", "expected=ready actual=failed"}},
		{name: "safe secret configuration identifiers",
			logs: []string{`ERROR failed to load config err="unknown field secretRotation"`,
				`ERROR failed to load config err="unknown field secretProvider"`},
			want: []string{`ERROR failed to load config err="unknown field secretRotation"`,
				`ERROR failed to load config err="unknown field secretProvider"`}},
		{name: "unstructured secret credentials",
			logs: []string{"startup secret private-value", "startup secretKey private-value", "startup secret_value private-value"}},
		{name: "masked secret field",
			logs: []string{"startup failed secret=private-value err=config"},
			want: []string{"startup failed secret=**** err=config"}},
		{name: "password and token", logs: []string{`startup failed password="private value" token=private-value stage=config`},
			want: []string{"startup failed password=**** token=**** stage=config"}},
		{name: "sensitive aliases", logs: []string{`startup failed passwd=private-value API_KEY='private value' access_token="private value" db_url=private-value`},
			want: []string{"startup failed passwd=**** API_KEY=**** ****=**** db_url=****"}},
		{name: "punctuation in sensitive values", logs: []string{"startup failed password=private,value token=private;value"},
			want: []string{"startup failed password=**** token=****"}},
		{name: "credential after punctuation", logs: []string{"startup failed (password=private,value) stage=config"},
			want: []string{"startup failed (password=**** stage=config"}},
		{name: "authorization header", logs: []string{"request failed Authorization: Bearer private-value status=401"},
			want: []string{"request failed Authorization: **** status=401"}},
		{name: "basic authorization", logs: []string{`request failed authorization="Basic private-value" status=401`},
			want: []string{"request failed authorization=**** status=401"}},
		{name: "DSNs", logs: []string{"connection failed dsn=private-value retry=1", "connection refused postgres://user:private-value@db/app"},
			want: []string{"connection failed dsn=**** retry=1", "connection refused postgres://****"}},
		{name: "credential URL", logs: []string{"request failed https://user:private-value@registry.example/image"},
			want: []string{"request failed https://****:****@registry.example/image"}},
		{name: "mixed diagnostic and sensitive fields", logs: []string{`ERROR failed to load config err="unknown configuration field" password=private-value token="private value"`},
			want: []string{`ERROR failed to load config err="unknown configuration field" password=**** token=****`}},
		{name: "unsupported credentials", logs: []string{"startup failed password private-value", "token: private-value", "startup failed"},
			want: []string{"startup failed"}},
		{name: "mixed supported and unsupported credentials", logs: []string{"startup failed password private-value token=private-value"}},
		{name: "unsupported authorization", logs: []string{"request failed Authorization: Custom private-value token=private-value"}},
		{name: "private key block", logs: []string{"startup failed\n-----BEGIN PRIVATE KEY-----\nprivate-value\n-----END PRIVATE KEY-----\nretry=1"},
			want: []string{"startup failed", "retry=1"}},
		{name: "split private key", logs: []string{"startup failed", "-----BEGIN RSA PRIVATE KEY-----", "private-value", "-----END RSA PRIVATE KEY-----", "retry=1"},
			want: []string{"startup failed", "retry=1"}},
		{name: "incomplete private key", logs: []string{"-----BEGIN EC PRIVATE KEY-----", "private-value"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := DiagnosticLogLines(tc.logs, MaxDiagnosticLogLines, MaxDiagnosticLogBytes)
			assert.Equal(t, tc.want, got)
			assert.NotContains(t, strings.Join(got, "\n"), "private-value")
			assert.NotContains(t, strings.Join(got, "\n"), "private value")
		})
	}
}

func TestDiagnosticLogLinesWithSlogTextHandler(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&output, nil))
	logger.ErrorContext(t.Context(), "failed to load config",
		"err", errors.New("unknown configuration field"), "file", "/etc/app.yaml", "password", "private value")
	lines := DiagnosticLogLines([]string{output.String()}, MaxDiagnosticLogLines, MaxDiagnosticLogBytes)
	assert.Len(t, lines, 1)
	text := strings.Join(lines, "\n")
	assert.Contains(t, text, `err="unknown configuration field"`)
	assert.Contains(t, text, "file=/etc/app.yaml")
	assert.Contains(t, text, "password=****")
	assert.NotContains(t, text, "private value")
}

func TestDiagnosticLogLinesLimits(t *testing.T) {
	for _, tc := range []struct {
		name     string
		logs     []string
		maxLines int
		maxBytes int
		want     []string
	}{
		{name: "line limit", logs: []string{strings.Repeat("state=failed\n", 21)}, maxLines: 20, maxBytes: 1600,
			want: strings.Split(strings.TrimSuffix(strings.Repeat("state=failed\n", 20), "\n"), "\n")},
		{name: "byte limit includes separators", logs: []string{"first", strings.Repeat("x", 1600)}, maxLines: 20, maxBytes: 1600,
			want: []string{"first", strings.Repeat("x", 1594)}},
		{name: "mask before truncation", logs: []string{"password=private-value err=failed"}, maxLines: 20, maxBytes: 11,
			want: []string{"password=**"}},
		{name: "zero lines", logs: []string{"state=failed"}, maxBytes: 1600},
		{name: "zero bytes", logs: []string{"state=failed"}, maxLines: 20},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := DiagnosticLogLines(tc.logs, tc.maxLines, tc.maxBytes)
			assert.Equal(t, tc.want, got)
			assert.LessOrEqual(t, len(got), tc.maxLines)
			assert.LessOrEqual(t, len(strings.Join(got, "\n")), tc.maxBytes)
		})
	}
}
