package compose

import (
	jsonv1 "encoding/json"
	"encoding/json/v2"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalizeCapabilities(t *testing.T) {
	tests := []struct {
		name     string
		add      Capabilities
		drop     Capabilities
		wantAdd  Capabilities
		wantDrop Capabilities
	}{
		{name: "empty lists stay nil"},
		{name: "canonical names are unchanged", add: Capabilities{"NET_ADMIN"}, wantAdd: Capabilities{"NET_ADMIN"}},
		{
			name:    "case, whitespace and prefix",
			add:     Capabilities{" net_admin ", "CAP_SYS_PTRACE", "cap_net_raw"},
			wantAdd: Capabilities{"NET_ADMIN", "NET_RAW", "SYS_PTRACE"},
		},
		{name: "duplicates", add: Capabilities{"NET_ADMIN", "cap_net_admin"}, wantAdd: Capabilities{"NET_ADMIN"}},
		{name: "reset is discarded", add: Capabilities{"RESET"}, drop: Capabilities{"reset", "MKNOD"}, wantDrop: Capabilities{"MKNOD"}},
		{name: "all replaces the list", add: Capabilities{"NET_ADMIN", "all"}, wantAdd: Capabilities{"ALL"}},
		{name: "all in drop", add: Capabilities{"CHOWN"}, drop: Capabilities{"MKNOD", "ALL"},
			wantAdd: Capabilities{"CHOWN"}, wantDrop: Capabilities{"ALL"}},
		{
			name:     "added capability is not dropped",
			add:      Capabilities{"NET_ADMIN"},
			drop:     Capabilities{"cap_net_admin", "sys_admin"},
			wantAdd:  Capabilities{"NET_ADMIN"},
			wantDrop: Capabilities{"SYS_ADMIN"},
		},
		{name: "drop emptied by add is nil", add: Capabilities{"NET_ADMIN"}, drop: Capabilities{"NET_ADMIN"},
			wantAdd: Capabilities{"NET_ADMIN"}},
		{name: "prefixed keywords are not keywords", add: Capabilities{"CAP_ALL", "cap_reset"},
			wantAdd: Capabilities{"CAP_ALL", "CAP_RESET"}},
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

	assert.Equal(t, Capabilities{"NET_ADMIN", "SYS_PTRACE"}, parsed.Services[0].CapAdd)
	assert.Equal(t, Capabilities{"ALL"}, parsed.Services[0].CapDrop)
	assert.Nil(t, parsed.Services[1].CapAdd)
	assert.Nil(t, parsed.Services[1].CapDrop)
}

func TestUnmarshalJSONNormalizesPersistedCapabilities(t *testing.T) {
	// Desired-state snapshots persisted before normalization keep capabilities as written.
	payload := []byte(`{"services":[
		{"name":"api","image":"api:1","cap_add":[" cap_sys_admin ","NET_ADMIN"],"cap_drop":["net_admin","mknod"]},
		{"name":"worker","image":"worker:1"}
	]}`)

	var desired Compose
	require.NoError(t, json.Unmarshal(payload, &desired, jsonv1.DefaultOptionsV1()))
	require.Len(t, desired.Services, 2)

	assert.Equal(t, "api", desired.Services[0].Name)
	assert.Equal(t, Capabilities{"NET_ADMIN", "SYS_ADMIN"}, desired.Services[0].CapAdd)
	assert.Equal(t, Capabilities{"MKNOD"}, desired.Services[0].CapDrop)
	assert.Nil(t, desired.Services[1].CapAdd)
	assert.Nil(t, desired.Services[1].CapDrop)

	encoded, err := json.Marshal(desired.Services[0], jsonv1.DefaultOptionsV1())
	require.NoError(t, err)
	assert.Contains(t, string(encoded), `"cap_add":["NET_ADMIN","SYS_ADMIN"]`)
}

func TestCapabilitiesDockerNames(t *testing.T) {
	assert.Nil(t, Capabilities(nil).DockerNames())
	assert.Equal(t, []string{"ALL"}, Capabilities{"ALL"}.DockerNames())
	assert.Equal(t, []string{"CAP_NET_ADMIN", "CAP_SYS_PTRACE"}, Capabilities{"SYS_PTRACE", "NET_ADMIN"}.DockerNames())
	assert.Equal(t, []string{"CAP_ALL"}, Capabilities{"CAP_ALL"}.DockerNames())
}
