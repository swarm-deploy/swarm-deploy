package tlschecker

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"io"
	"math/big"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const tlsTestName = "api.internal"

func TestTLSCertificateCheckValidation(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	ca := newTLSFixtureCA(t, now, "Root", nil)
	intermediate := newTLSFixtureCA(t, now, "Intermediate", ca)
	roots := x509.NewCertPool()
	roots.AddCert(ca.cert)

	tests := []struct {
		name, serverName, errorCode, warningCode string
		notBefore, notAfter                      time.Duration
		ips                                      []net.IP
		parent, sentIssuer                       *tlsFixtureCert
		usage                                    x509.ExtKeyUsage
		observeSNI                               bool
	}{
		{name: "valid", serverName: tlsTestName, parent: intermediate, sentIssuer: intermediate},
		{name: "expired", serverName: tlsTestName, notBefore: -48 * time.Hour, notAfter: -time.Hour,
			parent: ca, errorCode: "certificate_expired"},
		{name: "not yet valid", serverName: tlsTestName, notBefore: time.Hour,
			parent: ca, errorCode: "certificate_not_yet_valid"},
		{name: "self signed", serverName: tlsTestName, errorCode: "unknown_authority"},
		{name: "untrusted CA", serverName: tlsTestName, parent: newTLSFixtureCA(t, now, "Other Root", nil),
			errorCode: "unknown_authority"},
		{name: "name mismatch", serverName: "other.internal", parent: ca, errorCode: "hostname_mismatch"},
		{name: "omitted intermediate with no independent evidence", serverName: tlsTestName, parent: intermediate,
			errorCode: "unknown_authority"},
		{name: "IP SAN", parent: ca, ips: []net.IP{net.IPv4(127, 0, 0, 1)}, observeSNI: true},
		{name: "IP with SNI override", serverName: tlsTestName, parent: ca, observeSNI: true},
		{name: "client auth only", serverName: tlsTestName, parent: ca, usage: x509.ExtKeyUsageClientAuth,
			errorCode: "server_auth_incompatible"},
		{name: "expiring soon", serverName: tlsTestName, notAfter: 24 * time.Hour, parent: ca,
			warningCode: "certificate_expiring_soon"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			notBefore, notAfter := tt.notBefore, tt.notAfter
			if notBefore == 0 {
				notBefore = -time.Hour
			}
			if notAfter == 0 {
				notAfter = 60 * 24 * time.Hour
			}
			usage := tt.usage
			if usage == 0 {
				usage = x509.ExtKeyUsageServerAuth
			}
			template := &x509.Certificate{
				Subject: pkix.Name{CommonName: tlsTestName}, DNSNames: []string{tlsTestName}, IPAddresses: tt.ips,
				NotBefore: now.Add(notBefore), NotAfter: now.Add(notAfter), ExtKeyUsage: []x509.ExtKeyUsage{usage},
			}
			leaf := newTLSFixtureCert(t, template, tt.parent)
			keyPair := tls.Certificate{Certificate: [][]byte{leaf.cert.Raw}, PrivateKey: leaf.key}
			if tt.sentIssuer != nil {
				keyPair.Certificate = append(keyPair.Certificate, tt.sentIssuer.cert.Raw)
			}
			if tt.name == "untrusted CA" {
				keyPair.Certificate = append(keyPair.Certificate, tt.parent.cert.Raw)
			}
			sni := make(chan string, 1)
			config := &tls.Config{
				Certificates: []tls.Certificate{keyPair},
				MinVersion:   tls.VersionTLS13,
				GetCertificate: func(info *tls.ClientHelloInfo) (*tls.Certificate, error) {
					sni <- info.ServerName
					return &keyPair, nil
				},
			}
			if tt.observeSNI {
				config.Certificates = nil // Always invoke GetCertificate, including when the client sends no SNI.
			}
			port := serveTLSFixture(t, config)
			checker := New()
			checker.roots = roots
			result, err := checker.Check(t.Context(), Target{Host: "127.0.0.1", Port: &port, ServerName: tt.serverName})
			require.NoError(t, err)
			assert.True(t, result.Connection.TCPConnected)
			require.NotNil(t, result.Connection.HandshakeOK)
			assert.True(t, *result.Connection.HandshakeOK)
			assert.Equal(t, "TLS 1.3", result.Connection.TLSVersion)
			assert.NotEmpty(t, result.Connection.CipherSuite)
			assert.Equal(t, port, result.Connection.Port)
			require.NotNil(t, result.Certificate)
			assert.Equal(t, "CN="+tlsTestName, result.Certificate.Subject)
			assert.Equal(t, leaf.cert.Issuer.String(), result.Certificate.Issuer)
			assert.Equal(t, template.NotBefore, result.Certificate.NotBefore)
			assert.Equal(t, template.NotAfter, result.Certificate.NotAfter)
			fingerprint, err := hex.DecodeString(result.Certificate.SHA256Fingerprint)
			require.NoError(t, err)
			wantFingerprint := sha256.Sum256(leaf.cert.Raw)
			assert.Equal(t, wantFingerprint[:], fingerprint)
			assert.Equal(t, []string{tlsTestName}, result.Certificate.DNSSANs)
			assert.Equal(t, tt.parent == nil, result.Certificate.SelfSigned)
			require.NotNil(t, result.Chain)
			assert.Len(t, result.Chain.Presented, len(keyPair.Certificate))
			assert.Equal(t, len(keyPair.Certificate), result.Chain.PresentedCount)
			require.NotNil(t, result.Validation.HostnameMatches)
			require.NotNil(t, result.Validation.ValidityPeriodOK)
			assert.Equal(t, tt.errorCode != "hostname_mismatch", *result.Validation.HostnameMatches)
			assert.Equal(t, tt.errorCode != tlsCodeExpired && tt.errorCode != tlsCodeNotYetValid,
				*result.Validation.ValidityPeriodOK)
			if tt.errorCode == "" {
				assert.Empty(t, result.Errors)
				assert.Equal(t, "valid", result.Validation.Status)
				require.NotNil(t, result.Validation.ServerAuthOK)
				assert.True(t, *result.Validation.ServerAuthOK)
				assert.Equal(t, "verified", result.Chain.Status)
			} else {
				require.Len(t, result.Errors, 1)
				assert.Equal(t, tt.errorCode, result.Errors[0].Code)
				assert.Equal(t, "validation", result.Errors[0].Stage)
				assert.NotEmpty(t, result.Errors[0].Message)
				assert.Equal(t, "invalid", result.Validation.Status)
			}
			if tt.warningCode == "" {
				assert.Empty(t, result.Warnings)
			} else {
				require.Len(t, result.Warnings, 1)
				assert.Equal(t, tt.warningCode, result.Warnings[0].Code)
				assert.Positive(t, result.Certificate.RemainingSeconds)
			}
			if tt.observeSNI {
				select {
				case name := <-sni:
					assert.Equal(t, tt.serverName, name)
				default:
					t.Error("server did not get a ClientHello")
				}
				if tt.serverName == "" {
					assert.Equal(t, []string{"127.0.0.1"}, result.Certificate.IPSANs)
					assert.Equal(t, "127.0.0.1", result.Connection.ServerName)
				}
			}
		})
	}
}

func TestTLSMissingIntermediateRequiresVerifiedPath(t *testing.T) {
	now := time.Now()
	root := newTLSFixtureCA(t, now, "Root", nil)
	intermediate := newTLSFixtureCA(t, now, "Intermediate", root)
	leaf := newTLSFixtureCert(t, &x509.Certificate{
		Subject: pkix.Name{CommonName: tlsTestName}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour),
	}, intermediate)
	roots, intermediates := x509.NewCertPool(), x509.NewCertPool()
	roots.AddCert(root.cert)
	intermediates.AddCert(intermediate.cert)
	chains, err := leaf.cert.Verify(x509.VerifyOptions{Roots: roots, Intermediates: intermediates, CurrentTime: now})
	require.NoError(t, err)
	assert.True(t, tlsMissingIntermediate(chains, []*x509.Certificate{leaf.cert, root.cert}))
	assert.False(t, tlsMissingIntermediate(chains, []*x509.Certificate{leaf.cert, intermediate.cert}))
	assert.False(t, tlsMissingIntermediate(nil, []*x509.Certificate{leaf.cert}))
}

func TestTLSCertificateCheckNetworkFailures(t *testing.T) {
	tests := []struct {
		name, stage, code, reason string
		closeListener, sendPlain  bool
		cancel, cancelHandshake   bool
		timeout                   time.Duration
	}{
		{name: "connection refused", closeListener: true, stage: "connection", code: "connection_failed"},
		{name: "non TLS server", sendPlain: true, stage: "handshake", code: "tls_handshake_failed"},
		{name: "canceled", cancel: true, stage: "connection", code: "connection_failed", reason: "canceled"},
		{name: "handshake canceled", cancelHandshake: true, stage: "handshake",
			code: "tls_handshake_failed", reason: "canceled"},
		{name: "handshake timeout", timeout: 100 * time.Millisecond, stage: "handshake",
			code: "tls_handshake_failed", reason: "timeout"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			listener, err := net.Listen("tcp4", "127.0.0.1:0")
			require.NoError(t, err)
			t.Cleanup(func() { _ = listener.Close() })
			port := listener.Addr().(*net.TCPAddr).Port
			if tt.closeListener {
				require.NoError(t, listener.Close())
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			finished := make(chan struct{})
			defer close(finished)
			if tt.sendPlain || tt.cancelHandshake {
				go func() {
					conn, acceptErr := listener.Accept()
					if acceptErr != nil {
						return
					}
					defer conn.Close()
					if tt.sendPlain {
						_, _ = conn.Write([]byte("not a TLS handshake"))
					} else {
						// Wait for the ClientHello to ensure the dial completed before canceling.
						_, _ = io.ReadFull(conn, make([]byte, 1))
						cancel()
						<-finished
					}
				}()
			}
			if tt.cancel {
				cancel()
			}
			checker := New()
			if tt.timeout != 0 {
				checker.timeout = tt.timeout
			}
			result, err := checker.Check(ctx, Target{Host: "127.0.0.1", Port: &port})
			require.NoError(t, err)
			assert.Equal(t, tt.stage == "handshake", result.Connection.TCPConnected)
			if result.Connection.TCPConnected {
				require.NotNil(t, result.Connection.HandshakeOK)
				assert.False(t, *result.Connection.HandshakeOK)
			} else {
				assert.Nil(t, result.Connection.HandshakeOK)
			}
			assert.Nil(t, result.Certificate)
			assert.Nil(t, result.Chain)
			assert.Equal(t, tlsStatusNotChecked, result.Validation.Status)
			require.Len(t, result.Errors, 1)
			assert.Equal(t, tt.stage, result.Errors[0].Stage)
			assert.Equal(t, tt.code, result.Errors[0].Code)
			assert.Equal(t, tt.reason, result.Errors[0].Reason)
			assert.NotEmpty(t, result.Errors[0].Message)
		})
	}
}

func TestTLSCertificateCheckKeepsCertificateAfterHandshakeFailure(t *testing.T) {
	now := time.Now()
	root := newTLSFixtureCA(t, now, "Root", nil)
	leaf := newTLSFixtureCert(t, &x509.Certificate{
		Subject: pkix.Name{CommonName: tlsTestName}, DNSNames: []string{tlsTestName},
		NotBefore: now.Add(-2 * time.Hour), NotAfter: now.Add(-time.Hour),
	}, root)
	port := serveTLSFixture(t, &tls.Config{
		Certificates: []tls.Certificate{{Certificate: [][]byte{leaf.cert.Raw}, PrivateKey: leaf.key}},
		ClientAuth:   tls.RequireAnyClientCert,
		MinVersion:   tls.VersionTLS12,
		MaxVersion:   tls.VersionTLS12,
	})
	checker := New()
	checker.roots = x509.NewCertPool()
	checker.roots.AddCert(root.cert)
	result, err := checker.Check(t.Context(), Target{Host: "127.0.0.1", Port: &port, ServerName: tlsTestName})
	require.NoError(t, err)
	require.NotNil(t, result.Connection.HandshakeOK)
	assert.False(t, *result.Connection.HandshakeOK)
	require.NotNil(t, result.Certificate)
	require.Len(t, result.Errors, 2)
	assert.Equal(t, "tls_handshake_failed", result.Errors[0].Code)
	assert.Equal(t, "certificate_expired", result.Errors[1].Code)
	assert.Equal(t, "invalid", result.Validation.Status)
}

func TestParseTLSTarget(t *testing.T) {
	zero, high := 0, MaxPort+1
	tests := []struct {
		name, wantHost, wantIdentity string
		args                         Target
	}{
		{name: "DNS", args: Target{Host: " api.internal "}, wantHost: "api.internal", wantIdentity: "api.internal"},
		{name: "IPv6", args: Target{Host: "[fd00::1]"}, wantHost: "fd00::1", wantIdentity: "fd00::1"},
		{name: "scoped IPv6", args: Target{Host: "fe80::1%eth0"},
			wantHost: "fe80::1%eth0", wantIdentity: "fe80::1"},
		{name: "internal DNS", args: Target{Host: "stack_service"},
			wantHost: "stack_service", wantIdentity: "stack_service"},
		{name: "loopback DNS", args: Target{Host: "localhost"},
			wantHost: "localhost", wantIdentity: "localhost"},
		{name: "missing host"},
		{name: "URL", args: Target{Host: "https://api.internal"}},
		{name: "embedded port", args: Target{Host: "api.internal:8443"}},
		{name: "zero port", args: Target{Host: "api.internal", Port: &zero}},
		{name: "port too large", args: Target{Host: "api.internal", Port: &high}},
		{name: "invalid server name", args: Target{Host: "api.internal", ServerName: "*.internal"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.wantHost == "" {
				_, err := New().Check(t.Context(), tt.args)
				require.Error(t, err)
				return
			}
			got, err := parseTLSTarget(tt.args)
			require.NoError(t, err)
			assert.Equal(t, tlsDefaultPort, got.Port)
			assert.Equal(t, tt.wantHost, got.Host)
			assert.Equal(t, tt.wantIdentity, got.ServerName)
		})
	}
}

type tlsFixtureCert struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
}

func newTLSFixtureCA(t *testing.T, now time.Time, name string, issuer *tlsFixtureCert) *tlsFixtureCert {
	t.Helper()
	return newTLSFixtureCert(t, &x509.Certificate{
		Subject: pkix.Name{CommonName: name}, IsCA: true, BasicConstraintsValid: true,
		KeyUsage: x509.KeyUsageCertSign, NotBefore: now.Add(-72 * time.Hour), NotAfter: now.AddDate(1, 0, 0),
	}, issuer)
}

func newTLSFixtureCert(t *testing.T, template *x509.Certificate, issuer *tlsFixtureCert) *tlsFixtureCert {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	template.SerialNumber, err = rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	require.NoError(t, err)
	issuerCert, issuerKey := template, key
	if issuer != nil {
		issuerCert, issuerKey = issuer.cert, issuer.key
	}
	der, err := x509.CreateCertificate(rand.Reader, template, issuerCert, key.Public(), issuerKey)
	require.NoError(t, err)
	cert, err := x509.ParseCertificate(der)
	require.NoError(t, err)
	return &tlsFixtureCert{cert: cert, key: key}
}

func serveTLSFixture(t *testing.T, config *tls.Config) int {
	t.Helper()
	listener, err := tls.Listen("tcp4", "127.0.0.1:0", config)
	require.NoError(t, err)
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		defer conn.Close()
		_ = conn.(*tls.Conn).HandshakeContext(t.Context())
	}()
	t.Cleanup(func() {
		_ = listener.Close()
		<-done
	})
	return listener.Addr().(*net.TCPAddr).Port
}
