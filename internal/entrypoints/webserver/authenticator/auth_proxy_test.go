package authenticator

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAuthProxyAuthenticatorAuthenticatesUserFromHeader(t *testing.T) {
	auth, err := newAuthProxyAuthenticator("X-User-Name")
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-User-Name", " developer ")

	user, authenticated := auth.Authenticate(req)

	require.True(t, authenticated)
	assert.Equal(t, "developer", user.Name)
}

func TestAuthProxyAuthenticatorRejectsRequestWithoutLoginHeader(t *testing.T) {
	auth, err := newAuthProxyAuthenticator("X-User-Name")
	require.NoError(t, err)

	user, authenticated := auth.Authenticate(httptest.NewRequest(http.MethodGet, "/", nil))

	assert.False(t, authenticated)
	assert.Empty(t, user.Name)
}

func TestAuthProxyAuthenticatorRequiresLoginHeaderConfiguration(t *testing.T) {
	auth, err := newAuthProxyAuthenticator(" ")

	require.Error(t, err)
	assert.Nil(t, auth)
}

func TestAuthProxyAuthenticatorChallengesWithUnauthorized(t *testing.T) {
	auth, err := newAuthProxyAuthenticator("X-User-Name")
	require.NoError(t, err)
	rec := httptest.NewRecorder()

	auth.Challenge(rec)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}
