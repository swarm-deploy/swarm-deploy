package selfheal

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
)

func TestProposalUsesWriteRepository(t *testing.T) {
	const github = "git@github.com:team/app.git"
	const gitlab = "git@gitlab.com:team/app.git"
	for _, tc := range []struct {
		name, syncRepo, pushRepo, branchURL string
	}{
		{"write override", gitlab, github, "https://github.com/team/app/tree/change"},
		{"inherited repository", github, "", "https://github.com/team/app/tree/change"},
		{"other write host", github, gitlab, "https://gitlab.com/team/app/-/tree/change"},
		{"local write repository", github, "/repo", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			push := &config.GitPushSpec{Repository: tc.pushRepo}
			service, err := New(&config.Config{}, false, config.GitSpec{Repository: tc.syncRepo, Push: push}, t.TempDir())
			require.NoError(t, err)
			assert.Equal(t, tc.branchURL, service.remote.BranchURL("change"))
		})
	}
}
