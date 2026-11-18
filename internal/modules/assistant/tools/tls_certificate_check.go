package tools

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

	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/assistant/tools/routing"
)

const (
	tlsConnectTimeout   = 5 * time.Second
	tlsHandshakeTimeout = 5 * time.Second
	tlsDefaultPort      = 443
	tlsMaxPort          = 65535
	tlsMaxDNSName       = 253
	tlsMaxDNSLabel      = 63
)

// TLSCertificateCheck inspects one TLS endpoint from the swarm-deploy process.
type TLSCertificateCheck struct {
	timeout time.Duration
	roots   *x509.CertPool // nil selects the system trust store.
}

type tlsCertificateCheckRequest struct {
	// Host is the DNS name or IP address to connect to.
	Host string `json:"host"`
	// Port is the optional TCP port. The default is 443.
	Port *int `json:"port"`
	// ServerName overrides the TLS identity and DNS SNI.
	ServerName string `json:"server_name"`
}

type tlsCheckResult struct {
	// Connection describes the endpoint and the network phases reached.
	Connection tlsCheckConnection `json:"connection"`
	// Certificate contains the leaf certificate details, when available.
	Certificate *tlsLeafCertificate `json:"certificate,omitempty"`
	// Chain describes certificates presented by the endpoint and path verification.
	Chain *tlsCheckChain `json:"chain,omitempty"`
	// Validation describes the independent and overall certificate checks.
	Validation tlsCheckValidation `json:"validation"`
	// Warnings are non-fatal findings.
	Warnings []tlsCheckIssue `json:"warnings,omitempty"`
	// Errors contain the failing phase and machine-readable cause.
	Errors []tlsCheckIssue `json:"errors,omitempty"`
}

type tlsCheckConnection struct {
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

type tlsCheckIssue struct {
	// Stage identifies where the issue occurred.
	Stage string `json:"stage"`
	// Code identifies the issue independently of its human-readable text.
	Code string `json:"code"`
	// Message contains a bounded diagnostic description.
	Message string `json:"message"`
	// Reason distinguishes timed out or canceled network operations.
	Reason string `json:"reason,omitempty"`
}

// NewTLSCertificateCheck creates tls_certificate_check with the system trust store.
func NewTLSCertificateCheck() *TLSCertificateCheck {
	return &TLSCertificateCheck{timeout: tlsHandshakeTimeout}
}

// Definition returns the TLS tool's schema.
func (t *TLSCertificateCheck) Definition() routing.ToolDefinition {
	return routing.ToolDefinition{
		Name: config.AssistantToolNameTLSCertificateCheck,
		Description: "Performs a direct TLS handshake to inspect an endpoint's certificate, expiry, identity, " +
			"system trust chain, and negotiated protocol. Use a single DNS name or IP supplied by the user or service metadata.",
		ParametersJSONSchema: map[string]any{
			"type":     "object",
			"required": []string{"host"},
			"properties": map[string]any{
				"host": map[string]any{
					"type":        "string",
					"description": "DNS name or IP address without a scheme, path, or port.",
				},
				"port": map[string]any{
					"type":        "integer",
					"minimum":     1,
					"maximum":     tlsMaxPort,
					"description": "TCP port, defaults to 443.",
				},
				"server_name": map[string]any{
					"type":        "string",
					"description": "Optional SNI and certificate identity. Defaults to host. Use a DNS name for SNI over an IP.",
				},
			},
			"additionalProperties": false,
		},
		Request: tlsCertificateCheckRequest{},
	}
}

// Execute checks the endpoint and returns available certificate diagnostics even if verification fails.
func (t *TLSCertificateCheck) Execute(ctx context.Context, request routing.Request) (routing.Response, error) {
	args, err := convertRequestPayload[tlsCertificateCheckRequest](request.Payload)
	if err != nil {
		return routing.Response{}, err
	}
	target, err := parseTLSTarget(args)
	if err != nil {
		return routing.Response{}, err
	}
	result := t.check(ctx, target)
	return routing.Response{Payload: result}, nil
}

func (t *TLSCertificateCheck) check(ctx context.Context, target tlsCheckConnection) tlsCheckResult {
	result := tlsCheckResult{
		Connection: target,
		Validation: tlsCheckValidation{Status: tlsStatusNotChecked},
	}
	address := net.JoinHostPort(target.Host, strconv.Itoa(target.Port))
	dialer := net.Dialer{Timeout: tlsConnectTimeout}
	conn, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		result.Errors = append(result.Errors, tlsNetworkIssue("connection", "connection_failed", err))
		return result
	}
	defer conn.Close()
	result.Connection.TCPConnected = true

	// Verification below uses the actual identity and system roots. No application data is exchanged.
	client := tls.Client(conn, &tls.Config{
		ServerName:         target.ServerName,
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: true, //nolint:gosec // Preserve peer certificates and explicitly verify them below.
	})
	handshakeCtx, cancel := context.WithTimeout(ctx, t.timeout)
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
		result.inspectCertificates(state.PeerCertificates, t.roots, time.Now().UTC())
	}
	return result
}

func parseTLSTarget(args tlsCertificateCheckRequest) (tlsCheckConnection, error) {
	host := tlsTrimIPBrackets(strings.TrimSpace(args.Host))
	hostIP, err := netip.ParseAddr(host)
	if err != nil && !validTLSDNSName(host) {
		return tlsCheckConnection{}, errors.New("host must be a DNS name or IP address without a port")
	}
	port := tlsDefaultPort
	if args.Port != nil {
		port = *args.Port
		if port < 1 || port > tlsMaxPort {
			return tlsCheckConnection{}, fmt.Errorf("port must be between 1 and %d", tlsMaxPort)
		}
	}
	serverName := tlsTrimIPBrackets(strings.TrimSpace(args.ServerName))
	if serverName == "" {
		serverName = host
	}
	if ip, ipErr := netip.ParseAddr(serverName); ipErr == nil {
		serverName = ip.WithZone("").String()
	} else if !validTLSDNSName(serverName) {
		return tlsCheckConnection{}, errors.New("server_name must be a DNS name or IP address")
	}
	if hostIP.IsValid() {
		host = hostIP.String()
	}
	return tlsCheckConnection{Host: host, Port: port, ServerName: serverName}, nil
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

func tlsNetworkIssue(stage, code string, err error) tlsCheckIssue {
	issue := tlsCheckIssue{Stage: stage, Code: code, Message: tlsBoundText(err.Error())}
	var networkErr net.Error
	switch {
	case errors.Is(err, context.Canceled):
		issue.Reason = "canceled"
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &networkErr) && networkErr.Timeout():
		issue.Reason = "timeout"
	}
	return issue
}
