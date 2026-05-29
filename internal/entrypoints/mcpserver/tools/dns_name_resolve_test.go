package tools

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/entrypoints/mcpserver/routing"
)

func TestDNSNameResolveExecute(t *testing.T) {
	tool := NewDNSNameResolve()
	tool.resolver = &fakeDNSResolver{
		addresses: []net.IPAddr{
			{
				IP: net.ParseIP("10.10.10.10"),
			},
			{
				IP: net.ParseIP("2001:db8::1"),
			},
		},
	}

	response, err := tool.Execute(context.Background(), routing.Request{
		Payload: dnsNameResolveRequest{
			Name: "api.example.com",
		},
	})
	require.NoError(t, err, "execute dns_name_resolve")

	var payload struct {
		Name      string   `json:"name"`
		Addresses []string `json:"addresses"`
		Count     int      `json:"count"`
	}
	encoded, err := json.Marshal(response.Payload)
	require.NoError(t, err, "encode response payload")
	require.NoError(t, json.Unmarshal(encoded, &payload), "decode response payload")

	assert.Equal(t, "api.example.com", payload.Name, "unexpected name")
	assert.Equal(t, []string{"10.10.10.10", "2001:db8::1"}, payload.Addresses, "unexpected addresses")
	assert.Equal(t, 2, payload.Count, "unexpected count")
}

func TestDNSNameResolveExecuteRequiresName(t *testing.T) {
	tool := NewDNSNameResolve()

	_, err := tool.Execute(context.Background(), routing.Request{
		Payload: dnsNameResolveRequest{},
	})
	require.Error(t, err, "expected required name error")
	assert.Contains(t, err.Error(), "name is required", "unexpected error")
}

func TestDNSNameResolveExecuteNameMustBeString(t *testing.T) {
	tool := NewDNSNameResolve()

	_, err := tool.Execute(context.Background(), routing.Request{
		Payload: map[string]any{
			"name": 123,
		},
	})
	require.Error(t, err, "expected name type error")
	assert.Contains(t, err.Error(), "request payload has unexpected type", "unexpected error")
}

func TestDNSNameResolveExecuteResolveError(t *testing.T) {
	tool := NewDNSNameResolve()
	tool.resolver = &fakeDNSResolver{
		err: errors.New("no such host"),
	}

	_, err := tool.Execute(context.Background(), routing.Request{
		Payload: dnsNameResolveRequest{
			Name: "missing.example.com",
		},
	})
	require.Error(t, err, "expected resolve error")
	assert.Contains(t, err.Error(), "resolve dns name", "unexpected error")
}

type fakeDNSResolver struct {
	addresses []net.IPAddr
	err       error
}

func (f *fakeDNSResolver) LookupIPAddr(_ context.Context, _ string) ([]net.IPAddr, error) {
	if f.err != nil {
		return nil, f.err
	}

	out := make([]net.IPAddr, len(f.addresses))
	copy(out, f.addresses)

	return out, nil
}
