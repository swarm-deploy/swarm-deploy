package tools

import (
	"context"

	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/assistant/tools/routing"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/tlschecker"
)

// TLSCertificateCheck exposes endpoint certificate diagnostics as an assistant tool.
type TLSCertificateCheck struct {
	checker *tlschecker.Checker
}

// NewTLSCertificateCheck creates tls_certificate_check with the system trust store.
func NewTLSCertificateCheck() *TLSCertificateCheck {
	return &TLSCertificateCheck{checker: tlschecker.New()}
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
					"maximum":     tlschecker.MaxPort,
					"description": "TCP port, defaults to 443.",
				},
				"server_name": map[string]any{
					"type":        "string",
					"description": "Optional SNI and certificate identity. Defaults to host. Use a DNS name for SNI over an IP.",
				},
			},
			"additionalProperties": false,
		},
		Request: tlschecker.Target{},
	}
}

// Execute checks the endpoint and returns available certificate diagnostics even if verification fails.
func (t *TLSCertificateCheck) Execute(ctx context.Context, request routing.Request) (routing.Response, error) {
	args, err := convertRequestPayload[tlschecker.Target](request.Payload)
	if err != nil {
		return routing.Response{}, err
	}
	result, err := t.checker.Check(ctx, args)
	if err != nil {
		return routing.Response{}, err
	}
	return routing.Response{Payload: result}, nil
}
