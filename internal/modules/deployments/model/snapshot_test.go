package model

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPublicChangeFieldsHideGoModelRepresentation(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		expected string
	}{
		{name: "environment map", path: "environment/Map/MODE", expected: "environment.MODE"},
		{name: "command args", path: "command/Args/0", expected: "command[0]"},
		{name: "inline extra", path: "Extra/entrypoint/Args/0", expected: "entrypoint[0]"},
		{name: "volume wrappers", path: "volumes/Volumes/0/ReadOnly", expected: "volumes[0].read_only"},
		{name: "network wrappers", path: "networks/List/0/IPV4Address", expected: "networks[0].ipv4_address"},
		{name: "label named like parser metadata", path: "labels/Map/AliasMap", expected: "labels.alias_map"},
		{name: "environment named like parser metadata", path: "environment/Map/Keys", expected: "environment.keys"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, publicField(strings.Split(tt.path, "/")))
		})
	}
}
