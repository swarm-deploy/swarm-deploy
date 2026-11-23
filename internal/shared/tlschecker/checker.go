// Package tlschecker inspects and verifies TLS endpoint certificates.
package tlschecker

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"time"
)

const (
	tlsConnectTimeout   = 5 * time.Second
	tlsHandshakeTimeout = 5 * time.Second
	tlsDefaultPort      = 443
	tlsMaxDNSName       = 253
	tlsMaxDNSLabel      = 63
)

// MaxPort is the highest valid TCP port.
const MaxPort = 65535

// Checker inspects TLS endpoints with the default network timeouts.
type Checker struct {
	timeout time.Duration
	roots   *x509.CertPool // nil selects the system trust store.
}

// Target identifies the endpoint and optional TLS identity to check.
type Target struct {
	// Host is the DNS name or IP address to connect to.
	Host string `json:"host"`
	// Port is the TCP port. A nil port defaults to 443.
	Port *int `json:"port"`
	// ServerName overrides the TLS identity and DNS SNI.
	ServerName string `json:"server_name"`
}

// Result reports the network phases reached and any available certificate diagnostics.
type Result struct {
	// Connection describes the endpoint and the network phases reached.
	Connection Connection `json:"connection"`
	// Certificate contains the leaf certificate details, when available.
	Certificate *LeafCertificate `json:"certificate,omitempty"`
	// Chain describes certificates presented by the endpoint and path verification.
	Chain *Chain `json:"chain,omitempty"`
	// Validation describes the independent and overall certificate checks.
	Validation Validation `json:"validation"`
	// Warnings are non-fatal findings.
	Warnings []Issue `json:"warnings,omitempty"`
	// Errors contain the failing phase and machine-readable cause.
	Errors []Issue `json:"errors,omitempty"`
}

// Connection describes the normalized endpoint and the connection outcome.
type Connection struct {
	// Host is the DNS name or IP address used for TCP.
	Host string `json:"host"`
	// Port is the effective TCP port.
	Port int `json:"port"`
	// ServerName is the identity to verify and, for DNS names, the SNI.
	ServerName string `json:"server_name"`
	// TCPConnected reports whether a TCP connection was established.
	TCPConnected bool `json:"tcp_connected"`
	// HandshakeOK is absent until a TLS handshake is attempted.
	HandshakeOK *bool `json:"handshake_ok,omitempty"`
	// TLSVersion is the negotiated TLS version after a successful handshake.
	TLSVersion string `json:"tls_version,omitempty"`
	// CipherSuite is the negotiated cipher suite after a successful handshake.
	CipherSuite string `json:"cipher_suite,omitempty"`
}

// Issue describes a diagnostic error or warning.
type Issue struct {
	// Stage identifies where the issue occurred.
	Stage string `json:"stage"`
	// Code identifies the issue independently of its human-readable text.
	Code string `json:"code"`
	// Message contains a bounded diagnostic description.
	Message string `json:"message"`
	// Reason distinguishes timed out or canceled network operations.
	Reason string `json:"reason,omitempty"`
}

// New creates a checker that uses the system trust store.
func New() *Checker {
	return &Checker{timeout: tlsHandshakeTimeout}
}

// Check inspects the endpoint. Invalid input returns an error, while network and
// certificate failures are reported in Result.Errors.
func (c *Checker) Check(ctx context.Context, target Target) (Result, error) {
	connection, err := parseTLSTarget(target)
	if err != nil {
		return Result{}, err
	}
	result := Result{
		Connection: connection,
		Validation: Validation{Status: tlsStatusNotChecked},
	}
	address := net.JoinHostPort(connection.Host, strconv.Itoa(connection.Port))
	dialer := net.Dialer{Timeout: tlsConnectTimeout}
	conn, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		result.Errors = append(result.Errors, tlsNetworkIssue("connection", "connection_failed", err))
		return result, nil
	}
	defer conn.Close()
	result.Connection.TCPConnected = true

	// Verification below uses the actual identity and system roots. No application data is exchanged.
	client := tls.Client(conn, &tls.Config{
		ServerName:         connection.ServerName,
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: true, //nolint:gosec // Preserve peer certificates and explicitly verify them below.
	})
	handshakeCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	handshakeErr := client.HandshakeContext(handshakeCtx)
	state := client.ConnectionState()
	result.Connection.HandshakeOK = &state.HandshakeComplete
	if handshakeErr != nil {
		result.Errors = append(result.Errors, tlsNetworkIssue("handshake", "tls_handshake_failed", handshakeErr))
	} else {
		result.Connection.TLSVersion = tls.VersionName(state.Version)
		result.Connection.CipherSuite = tls.CipherSuiteName(state.CipherSuite)
	}
	if len(state.PeerCertificates) > 0 {
		result.inspectCertificates(state.PeerCertificates, c.roots, time.Now().UTC())
	}
	return result, nil
}

func parseTLSTarget(args Target) (Connection, error) {
	host := tlsTrimIPBrackets(strings.TrimSpace(args.Host))
	hostIP, err := netip.ParseAddr(host)
	if err != nil && !validTLSDNSName(host) {
		return Connection{}, errors.New("host must be a DNS name or IP address without a port")
	}
	port := tlsDefaultPort
	if args.Port != nil {
		port = *args.Port
		if port < 1 || port > MaxPort {
			return Connection{}, fmt.Errorf("port must be between 1 and %d", MaxPort)
		}
	}
	serverName := tlsTrimIPBrackets(strings.TrimSpace(args.ServerName))
	if serverName == "" {
		serverName = host
	}
	if ip, ipErr := netip.ParseAddr(serverName); ipErr == nil {
		serverName = ip.WithZone("").String()
	} else if !validTLSDNSName(serverName) {
		return Connection{}, errors.New("server_name must be a DNS name or IP address")
	}
	if hostIP.IsValid() {
		host = hostIP.String()
	}
	return Connection{Host: host, Port: port, ServerName: serverName}, nil
}

func tlsTrimIPBrackets(name string) string {
	if strings.HasPrefix(name, "[") && strings.HasSuffix(name, "]") && strings.Contains(name, ":") {
		return name[1 : len(name)-1]
	}
	return name
}

func validTLSDNSName(name string) bool {
	name = strings.TrimSuffix(name, ".")
	if name == "" || len(name) > tlsMaxDNSName {
		return false
	}
	for label := range strings.SplitSeq(name, ".") {
		if label == "" || len(label) > tlsMaxDNSLabel {
			return false
		}
		for _, c := range label {
			switch {
			case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '_':
			default:
				return false
			}
		}
	}
	return true
}

func tlsNetworkIssue(stage, code string, err error) Issue {
	issue := Issue{Stage: stage, Code: code, Message: tlsBoundText(err.Error())}
	var networkErr net.Error
	switch {
	case errors.Is(err, context.Canceled):
		issue.Reason = "canceled"
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &networkErr) && networkErr.Timeout():
		issue.Reason = "timeout"
	}
	return issue
}
