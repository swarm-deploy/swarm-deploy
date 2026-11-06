package converter

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer/resources"
)

func TestExtraHostsIPv6(t *testing.T) {
	for _, tc := range []struct {
		name, hosts, want string
	}{
		{"bracketed IPv6 equals", `['db=[2001:db8::1]']`, "2001:db8::1 db"},
		{"bracketed IPv6 colon", `['db:[2001:db8::1]']`, "2001:db8::1 db"},
		{"IPv6 colon", `['db:2001:db8::1']`, "2001:db8::1 db"},
		{"IPv6 equals", `['db=2001:db8::1']`, "2001:db8::1 db"},
		{"IPv4 equals", `['db=127.0.0.1']`, "127.0.0.1 db"},
		{"IPv4 colon", `['db:127.0.0.1']`, "127.0.0.1 db"},
		{"mapping IPv6", `{db: '2001:db8::1'}`, "2001:db8::1 db"},
		{"mapping bracketed IPv6", `{db: '[2001:db8::1]'}`, "2001:db8::1 db"},
		{"preserves IPv6 spelling", `['db=[2001:0DB8:0:0:0:0:0:1]']`, "2001:0DB8:0:0:0:0:0:1 db"},
		{"malformed IPv6", `['db=[2001:db8::invalid]']`, "[2001:db8::invalid] db"},
		{"missing closing bracket", `['db=[2001:db8::1']`, "[2001:db8::1 db"},
		{"missing opening bracket", `['db=2001:db8::1]']`, "2001:db8::1] db"},
		{"nested brackets", `['db=[[2001:db8::1]]']`, "[[2001:db8::1]] db"},
		{"empty brackets", `['db=[]']`, "[] db"},
		{"bracketed IPv4", `['db=[127.0.0.1]']`, "[127.0.0.1] db"},
		{"bracketed hostname", `['db=[localhost]']`, "[localhost] db"},
		{"scoped address", `['db=[fe80::1%eth0]']`, "[fe80::1%eth0] db"},
		{"address with port", `['db=[2001:db8::1]:5432']`, "[2001:db8::1]:5432 db"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			desired := parseStack(t, "services: {api: {image: alpine, extra_hosts: "+tc.hosts+"}}")
			spec, err := convertService("demo", desired.Services[0], nil,
				[]NetworkSpec{{Alias: "default", ID: "net"}}, resources.Resolved{})
			require.NoError(t, err)
			assert.Equal(t, []string{tc.want}, spec.TaskTemplate.ContainerSpec.Hosts)
		})
	}
}
