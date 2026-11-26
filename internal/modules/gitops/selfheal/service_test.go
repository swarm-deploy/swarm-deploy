package selfheal

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
)

func TestGitHubPublicationUsesWriteRepositoryAndToken(t *testing.T) {
	const github = "git@github.com:team/app.git"
	const gitlab = "git@gitlab.com:team/app.git"
	for _, tc := range []struct {
		name, syncRepo, pushRepo, token, branchURL string
		wantPR                                     bool
	}{
		{"write override", gitlab, github, "write-token", "https://github.com/team/app/tree/change", true},
		{"inherited repository", github, "", "write-token", "https://github.com/team/app/tree/change", true},
		{"no API token", gitlab, github, "", "https://github.com/team/app/tree/change", false},
		{"other write host", github, gitlab, "write-token", "https://gitlab.com/team/app/-/tree/change", false},
		{"local write repository", github, "/repo", "write-token", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			push := &config.GitPushSpec{Repository: tc.pushRepo}
			push.APIToken.Content = []byte(tc.token)
			service, err := New(&config.Config{}, false, config.GitSpec{Repository: tc.syncRepo, Push: push}, t.TempDir())
			require.NoError(t, err)
			assert.Equal(t, tc.wantPR, service.changeRequests != nil)
			assert.Equal(t, tc.branchURL, service.repo.BranchURL("change"))
		})
	}
}
