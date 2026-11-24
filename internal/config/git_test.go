package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGitWriteCredentials(t *testing.T) {
	syncAuth := GitAuthSpec{Type: GitAuthTypeSSH}
	writeAuth := GitAuthSpec{Type: GitAuthTypeHTTP}
	for _, tc := range []struct {
		name string
		push *GitPushSpec
		want GitAuthSpec
	}{
		{"default", nil, syncAuth},
		{"other branch", &GitPushSpec{Branch: "proposals"}, syncAuth},
		{"same repository", &GitPushSpec{Repository: "sync"}, syncAuth},
		{"other repository", &GitPushSpec{Repository: "write"}, GitAuthSpec{}},
		{"explicit credentials", &GitPushSpec{Repository: "write", Auth: &writeAuth}, writeAuth},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spec := GitSpec{Repository: "sync", Branch: "main", Auth: syncAuth, Push: tc.push}
			write, _ := spec.ForPush()
			assert.Equal(t, tc.want, write.Auth)
			assert.Equal(t, syncAuth, spec.Auth)
		})
	}
}
