package authenticator

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGitHubAuthenticatorAuthenticate(t *testing.T) {
	body := []byte(`{"ref":"refs/heads/main"}`)
	secret := []byte("webhook-secret")

	mac := hmac.New(sha256.New, secret)
	_, err := mac.Write(body)
	require.NoError(t, err)

	validSignature := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	tests := []struct {
		name      string
		signature string
		body      []byte
		wantErr   bool
	}{
		{name: "valid signature", signature: validSignature, body: body},
		{name: "different body", signature: validSignature, body: []byte(`{"ref":"refs/heads/dev"}`), wantErr: true},
		{name: "unsupported algorithm", signature: "sha1=deadbeef", body: body, wantErr: true},
		{name: "invalid hex", signature: "sha256=not-hex", body: body, wantErr: true},
		{name: "missing signature", body: body, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/webhook", nil)
			req.Header.Set("X-Hub-Signature-256", tt.signature)

			err := NewGitHubAuthenticator(secret).Authenticate(&Request{
				Request: req,
				Body:    tt.body,
			})

			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
		})
	}
}
