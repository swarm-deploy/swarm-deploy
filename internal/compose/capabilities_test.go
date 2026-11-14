package compose

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalizeCapabilities(t *testing.T) {
	tests := []struct {
		name     string
		add      []string
		drop     []string
		wantAdd  []string
		wantDrop []string
	}{
		{name: "empty lists stay nil"},
		{name: "canonical names are unchanged", add: []string{"NET_ADMIN"}, wantAdd: []string{"NET_ADMIN"}},
		{
			name:    "case, whitespace and prefix",
			add:     []string{" net_admin ", "CAP_SYS_PTRACE", "cap_net_raw"},
			wantAdd: []string{"NET_ADMIN", "NET_RAW", "SYS_PTRACE"},
		},
		{name: "duplicates", add: []string{"NET_ADMIN", "cap_net_admin"}, wantAdd: []string{"NET_ADMIN"}},
		{name: "reset is discarded", add: []string{"RESET"}, drop: []string{"reset", "MKNOD"}, wantDrop: []string{"MKNOD"}},
		{name: "all replaces the list", add: []string{"NET_ADMIN", "all"}, wantAdd: []string{"ALL"}},
		{name: "all in drop", add: []string{"CHOWN"}, drop: []string{"MKNOD", "ALL"},
			wantAdd: []string{"CHOWN"}, wantDrop: []string{"ALL"}},
		{
			name:     "added capability is not dropped",
			add:      []string{"NET_ADMIN"},
			drop:     []string{"cap_net_admin", "sys_admin"},
			wantAdd:  []string{"NET_ADMIN"},
			wantDrop: []string{"SYS_ADMIN"},
		},
		{name: "drop emptied by add is nil", add: []string{"NET_ADMIN"}, drop: []string{"NET_ADMIN"},
			wantAdd: []string{"NET_ADMIN"}},
		{name: "prefixed keywords are not keywords", add: []string{"CAP_ALL", "cap_reset"},
			wantAdd: []string{"CAP_ALL", "CAP_RESET"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			add, drop := NormalizeCapabilities(tt.add, tt.drop)
			assert.Equal(t, tt.wantAdd, add)
			assert.Equal(t, tt.wantDrop, drop)

			againAdd, againDrop := NormalizeCapabilities(add, drop)
			assert.Equal(t, add, againAdd, "normalization must be idempotent")
			assert.Equal(t, drop, againDrop, "normalization must be idempotent")
		})
	}
}

func TestParseNormalizesCapabilities(t *testing.T) {
	parsed, err := Parse([]byte(`
services:
  api:
    image: api:1
    cap_add: [cap_net_admin, SYS_PTRACE, net_admin]
    cap_drop: [ALL]
  worker:
    image: worker:1
`))
	require.NoError(t, err)
	require.Len(t, parsed.Services, 2)

	assert.Equal(t, []string{"NET_ADMIN", "SYS_PTRACE"}, parsed.Services[0].CapAdd)
	assert.Equal(t, []string{"ALL"}, parsed.Services[0].CapDrop)
	assert.Nil(t, parsed.Services[1].CapAdd)
	assert.Nil(t, parsed.Services[1].CapDrop)
}
