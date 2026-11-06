package faults

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDiagnosticLogLines(t *testing.T) {
	for _, tc := range []struct {
		name string
		logs []string
		want []string
	}{
		{name: "short ordinary messages", logs: []string{"INFO", "true", "node", "starting"},
			want: []string{"INFO", "true", "node", "starting"}},
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
		{name: "byte limit includes separators", logs: []string{"first", strings.Repeat(".", 1600)}, maxLines: 20, maxBytes: 1600,
			want: []string{"first", strings.Repeat(".", 1594)}},
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

func TestDiagnosticLogLinesPEMFragments(t *testing.T) {
	// Generate disposable keys so the fixtures exercise real PEM line lengths and tails.
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	ecKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	ecDER, err := x509.MarshalECPrivateKey(ecKey)
	require.NoError(t, err)
	pkcs8DER, err := x509.MarshalPKCS8PrivateKey(rsaKey)
	require.NoError(t, err)

	for _, key := range []struct {
		name string
		der  []byte
	}{
		{name: "RSA PRIVATE KEY", der: x509.MarshalPKCS1PrivateKey(rsaKey)},
		{name: "EC PRIVATE KEY", der: ecDER},
		{name: "PRIVATE KEY", der: pkcs8DER},
	} {
		t.Run(key.name, func(t *testing.T) {
			lines := strings.Split(strings.TrimSpace(string(pem.EncodeToMemory(&pem.Block{
				Type: key.name, Bytes: key.der,
			}))), "\n")
			body := lines[1 : len(lines)-1]
			for _, tc := range []struct {
				name string
				logs []string
			}{
				{name: "complete", logs: lines},
				{name: "without BEGIN", logs: lines[1:]},
				{name: "without END", logs: lines[:len(lines)-1]},
				{name: "without markers", logs: body},
				{name: "partial line", logs: []string{body[0][3:24]}},
				{name: "short fragment inside block", logs: []string{lines[0], body[0][20:24], lines[len(lines)-1]}},
				{name: "short padded tail", logs: []string{"YQ=="}},
			} {
				t.Run(tc.name, func(t *testing.T) {
					assert.Empty(t, DiagnosticLogLines(tc.logs, MaxDiagnosticLogLines, MaxDiagnosticLogBytes))
				})
			}
			for i, fragment := range body {
				assert.Empty(t, DiagnosticLogLines([]string{fragment}, MaxDiagnosticLogLines, MaxDiagnosticLogBytes),
					"standalone key line %d", i)
			}
			logs := append([]string{"startup failed"}, lines...)
			logs = append(logs, "retry=1")
			assert.Equal(t, []string{"startup failed", "retry=1"},
				DiagnosticLogLines(logs, MaxDiagnosticLogLines, MaxDiagnosticLogBytes))

			if key.name == "RSA PRIVATE KEY" {
				// Reproduce collectTaskLogs' last-20-lines window: 19 RSA body lines and END, no BEGIN.
				require.Greater(t, len(body), 19)
				window := lines[len(lines)-20:]
				require.Len(t, window, 20)
				assert.Empty(t, DiagnosticLogLines(window, MaxDiagnosticLogLines, MaxDiagnosticLogBytes))

				// The same body fragments must be removed even if neither marker is retrieved.
				window = append([]string{}, body[len(body)-19:]...)
				window = append(window, "startup failed: invalid configuration")
				require.Len(t, window, 20)
				assert.Equal(t, []string{"startup failed: invalid configuration"},
					DiagnosticLogLines(window, MaxDiagnosticLogLines, MaxDiagnosticLogBytes))
			}
		})
	}
}
