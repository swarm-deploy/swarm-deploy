package git

import (
	"errors"
	"net"
	"testing"

	"github.com/go-git/go-git/v5/plumbing/format/pktline"
	"github.com/go-git/go-git/v5/plumbing/transport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"golang.org/x/crypto/ssh/knownhosts"
)

func TestClassifyPullError(t *testing.T) {
	testCases := []struct {
		name       string
		err        error
		repository string
		authType   config.GitAuthType
		want       FailureCategory
	}{
		{name: "HTTP authentication", err: transport.ErrAuthenticationRequired, want: FailureAuthentication},
		{name: "access denied", err: transport.ErrAuthorizationFailed, want: FailureAccessDenied},
		{name: "repository not found", err: transport.ErrRepositoryNotFound, want: FailureRepositoryNotFound},
		{name: "network", err: &net.DNSError{Err: "temporary", Name: "git.example.com", IsTemporary: true}, want: FailureNetwork},
		{name: "SSH host verification", err: &knownhosts.KeyError{}, repository: "git@example.com:team/app.git", want: FailureSSHHostVerification},
		{name: "revoked SSH host key", err: &knownhosts.RevokedError{}, repository: "git@example.com:team/app.git", want: FailureSSHHostVerification},
		{name: "SSH authentication", err: errors.New("ssh: handshake failed: ssh: unable to authenticate"), authType: config.GitAuthTypeSSH, want: FailureAuthentication},
		{name: "SSH access denied", err: &pktline.ErrorLine{Text: "ERROR: Access denied"}, repository: "ssh://git@example.com/team/app.git", want: FailureAccessDenied},
		{name: "other", err: errors.New("unexpected Git error"), want: FailureOther},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			err := ClassifyPullError(testCase.err, testCase.repository, testCase.authType)
			category, diagnostic := PullFailure(err)
			assert.Equal(t, testCase.want, category)
			assert.NotEmpty(t, diagnostic)
			assert.NotContains(t, diagnostic, testCase.err.Error(), "diagnostic must not copy transport errors")
		})
	}
}

func TestClassifyPullErrorRemovesSecrets(t *testing.T) {
	secret := "top-secret-token"
	err := ClassifyPullError(errors.New("request https://robot:"+secret+"@git.example.com/team/app failed"),
		"https://robot:"+secret+"@git.example.com/team/app.git", config.GitAuthTypeHTTP)
	require.Error(t, err)
	assert.NotContains(t, err.Error(), secret)
	assert.NotContains(t, RepositoryIdentifier("https://robot:"+secret+"@git.example.com/team/app.git"), secret)
	assert.Equal(t, "git.example.com/team/app", RepositoryIdentifier(
		"https://robot:"+secret+"@git.example.com/team/app.git"))
}

func TestRepositoryIdentifierMatchesHTTPSAndSSHForms(t *testing.T) {
	assert.Equal(t,
		RepositoryIdentifier("https://git.example.com/team/app.git"),
		RepositoryIdentifier("git@git.example.com:team/app.git"),
	)
}

func TestRepositoryIdentifierMalformedURLIsOpaqueAndStable(t *testing.T) {
	const secret = "top-secret-token"
	for _, remote := range []string{
		"https://robot:" + secret + "%zz@git.example.com/team/app.git",
		"https:robot:" + secret + "@git.example.com/team/app.git",
		"https:/",
	} {
		identifier := RepositoryIdentifier(remote)
		assert.Equal(t, identifier, RepositoryIdentifier(remote))
		assert.Regexp(t, `^repository-[0-9a-f]{16}$`, identifier)
		assert.NotContains(t, identifier, secret)
		assert.NotContains(t, identifier, "robot")
	}
}
