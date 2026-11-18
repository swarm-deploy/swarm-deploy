package assistant

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
)

const tlsCheckTool = string(config.AssistantToolNameTLSCertificateCheck)

func TestTLSCertificateCheckThroughChat(t *testing.T) {
	endpoint := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("the TLS check sent an HTTP request")
	}))
	t.Cleanup(endpoint.Close)
	address := endpoint.Listener.Addr().(*net.TCPAddr)
	args := fmt.Sprintf(`{"host":%q,"port":%d}`, address.IP, address.Port)

	tests := []struct {
		name, message, decision, wantToolResult string
		allowedTools                            []string
		wantTLS                                 bool
	}{
		{
			name:           "Russian expiry lookup",
			message:        fmt.Sprintf("Когда истекает TLS-сертификат %s?", address),
			decision:       `{"route":"lookups","capabilities":["tls"],"operation":null}`,
			wantToolResult: "unknown_authority", wantTLS: true,
		},
		{
			name:           "English validation diagnosis",
			message:        fmt.Sprintf("Why does TLS certificate validation fail on %s?", address),
			decision:       `{"route":"diagnostics","capabilities":["tls"],"operation":null}`,
			allowedTools:   []string{tlsCheckTool},
			wantToolResult: "unknown_authority", wantTLS: true,
		},
		{
			name:     "TLS concept",
			message:  "What does the TLS handshake do?",
			decision: `{"route":"platform","capabilities":[],"operation":null}`,
		},
		{
			name:           "allowlist excludes TLS",
			message:        fmt.Sprintf("Check the TLS certificate on %s", address),
			decision:       `{"route":"lookups","capabilities":["tls"],"operation":null}`,
			allowedTools:   []string{"date"},
			wantToolResult: "tool is not allowed by the selected assistant capabilities",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			requests := make(chan capturedChatRequest, 3)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				captured := decodeCapturedChatRequest(t, req)
				requests <- captured
				w.Header().Set("Content-Type", "application/json")
				switch len(requests) {
				case 1:
					writeChatResponse(t, w, tt.decision, nil)
				case 2:
					if tt.wantToolResult == "" {
						writeChatResponse(t, w, "done", nil)
						return
					}
					call := configurationChatCall("tls-call", tlsCheckTool, args)
					writeChatResponse(t, w, "", []map[string]any{call})
				case 3:
					writeChatResponse(t, w, "done", nil)
				default:
					t.Error("unexpected model request")
				}
			}))
			t.Cleanup(server.Close)

			spec := config.Spec{Assistant: config.AssistantSpec{Tools: tt.allowedTools}}
			service := newConfigurationChatService(t, server.URL, spec)
			response := service.Chat(t.Context(), ChatRequest{Message: tt.message, WaitTimeoutMS: 5000})
			require.Equal(t, StatusCompleted, response.Status, response)
			assert.Equal(t, "done", response.Answer)
			expectedRequests := 2
			if tt.wantToolResult != "" {
				expectedRequests++
			}
			require.Len(t, requests, expectedRequests)
			router, generation := <-requests, <-requests
			assert.True(t, requestContains(router, tt.message))
			assert.True(t, requestContains(router, "tls: live certificate"))
			assert.True(t, requestContains(router, "Когда истекает TLS-сертификат"))
			assert.True(t, requestContains(router, "Why does TLS certificate validation fail"))
			if tt.wantTLS {
				assert.Contains(t, capturedToolNames(generation), tlsCheckTool)
				assert.NotContains(t, capturedToolNames(generation), "dns_name_resolve")
				assert.True(t, requestContains(generation, "Distinguish TCP, handshake, and certificate validation results"))
			} else {
				assert.NotContains(t, capturedToolNames(generation), tlsCheckTool)
			}
			if tt.wantToolResult != "" {
				result := messageForToolCall(<-requests, "tls-call")
				assert.Contains(t, result, tt.wantToolResult)
				if tt.wantTLS {
					assert.Contains(t, result, `"tcp_connected":true`)
					assert.Contains(t, result, `"handshake_ok":true`)
					assert.Contains(t, result, `"status":"invalid"`)
					assert.Contains(t, result, `"sha256_fingerprint"`)
				}
			}
		})
	}
}
