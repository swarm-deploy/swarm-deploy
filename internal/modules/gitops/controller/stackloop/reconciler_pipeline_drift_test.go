package stackloop

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSelfHealEnabled(t *testing.T) {
	tests := []struct {
		name         string
		labels       map[string]string
		globalPolicy bool
		want         bool
		wantErr      bool
	}{
		{name: "global fallback", globalPolicy: true, want: true},
		{name: "label enables", labels: map[string]string{selfHealLabel: "true"}, want: true},
		{name: "label disables global", labels: map[string]string{selfHealLabel: "false"}, globalPolicy: true},
		{name: "invalid label", labels: map[string]string{selfHealLabel: "maybe"}, wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := selfHealEnabled(test.labels, test.globalPolicy)
			if test.wantErr {
				assert.Error(t, err)
				return
			}

			assert.NoError(t, err)
			assert.Equal(t, test.want, got)
		})
	}
}
