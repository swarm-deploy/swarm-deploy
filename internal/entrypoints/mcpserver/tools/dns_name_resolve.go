package tools

import (
	"context"
	"fmt"
	"net"
	"strings"

	"github.com/swarm-deploy/swarm-deploy/internal/entrypoints/mcpserver/routing"
)

// DNSNameResolve resolves DNS names.
type DNSNameResolve struct {
	resolver DNSResolver
}

type dnsNameResolveRequest struct {
	Name string `json:"name"`
}

// NewDNSNameResolve creates dns_name_resolve component.
func NewDNSNameResolve() *DNSNameResolve {
	return &DNSNameResolve{
		resolver: net.DefaultResolver,
	}
}

// Definition returns tool metadata visible to the model.
func (d *DNSNameResolve) Definition() routing.ToolDefinition {
	return routing.ToolDefinition{
		Name:        "dns_name_resolve",
		Description: "Resolves a DNS name and returns resolved IP addresses.",
		ParametersJSONSchema: map[string]any{
			"type": "object",
			"required": []string{
				"name",
			},
			"properties": map[string]any{
				"name": map[string]any{
					"type":        "string",
					"description": "DNS name to resolve, for example api.example.com.",
				},
			},
		},
		Request: dnsNameResolveRequest{},
	}
}

// Execute runs dns_name_resolve tool.
func (d *DNSNameResolve) Execute(ctx context.Context, request routing.Request) (routing.Response, error) {
	parsedRequest, err := convertRequestPayload[dnsNameResolveRequest](request.Payload)
	if err != nil {
		return routing.Response{}, err
	}

	name, err := parseDNSName(parsedRequest.Name)
	if err != nil {
		return routing.Response{}, err
	}

	addresses, err := d.resolver.LookupIPAddr(ctx, name)
	if err != nil {
		return routing.Response{}, fmt.Errorf("resolve dns name %q: %w", name, err)
	}

	ipStrings := make([]string, 0, len(addresses))
	for _, address := range addresses {
		ipStrings = append(ipStrings, address.IP.String())
	}

	payload := struct {
		Name      string   `json:"name"`
		Addresses []string `json:"addresses"`
		Count     int      `json:"count"`
	}{
		Name:      name,
		Addresses: ipStrings,
		Count:     len(ipStrings),
	}

	return routing.Response{
		Payload: payload,
	}, nil
}

func parseDNSName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("name is required")
	}

	return name, nil
}
