package compose

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNumericInterpolationThroughMergeAnchors(t *testing.T) {
	for _, tc := range []struct {
		name, extensions, deploy string
		want                     uint64
	}{
		{name: "direct", deploy: "replicas: '${SD_MERGE_REPLICAS:-2}'", want: 2},
		{name: "merged", extensions: "x-deploy: &deployment\n  replicas: '${SD_MERGE_REPLICAS:-2}'\n", deploy: "<<: *deployment", want: 2},
		{name: "chained", extensions: "x-base: &base\n  replicas: '${SD_MERGE_REPLICAS:-2}'\nx-deploy: &deployment\n  <<: *base\n", deploy: "<<: *deployment", want: 2},
		{name: "nested", extensions: "x-update: &update\n  parallelism: '${SD_MERGE_REPLICAS:-2}'\nx-deploy: &deployment\n  replicas: '${SD_MERGE_REPLICAS:-2}'\n  update_config:\n    <<: *update\n", deploy: "<<: *deployment", want: 2},
		{name: "merge sequence precedence", extensions: "x-first: &first\n  replicas: '${SD_MERGE_REPLICAS:-2}'\nx-second: &second\n  replicas: 9\n", deploy: "<<: [*first, *second]", want: 2},
		{name: "explicit override", extensions: "x-deploy: &deployment\n  replicas: '${SD_MERGE_REPLICAS:-2}'\n", deploy: "<<: *deployment\n      replicas: 7", want: 7},
	} {
		t.Run(tc.name, func(t *testing.T) {
				t.Setenv("SD_MERGE_REPLICAS", "3")
				source := []byte(fmt.Sprintf("%sservices:\n  app:\n    image: nginx:alpine\n    deploy:\n      %s\n", tc.extensions, tc.deploy))
				loader := NewFileLoaderWithReader(func(context.Context, string) ([]byte, error) { return source, nil })
				file, err := loader.Load(t.Context(), "compose.yaml")
				// Expressions remain literal, so numeric fields cannot decode them.
				if tc.name != "explicit override" {
					require.Error(t, err)
					return
				}
				require.NoError(t, err)
				require.NotNil(t, file.Compose.Services[0].Deploy.Replicas)
				assert.EqualValues(t, 7, *file.Compose.Services[0].Deploy.Replicas)
			})
	}
}

func TestInterpolationKeepsAnchorValuesIndependentOfTypedUses(t *testing.T) {
	t.Setenv("SD_MERGE_REPLICAS", "3")
	source := []byte(`x-common: &common
  replicas: 3
services:
  z:
    image: nginx:alpine
    deploy:
      <<: *common
    environment:
      <<: *common
  a:
    image: nginx:alpine
    deploy:
      <<: *common
      replicas: 4
`)
	loader := NewFileLoaderWithReader(func(context.Context, string) ([]byte, error) { return source, nil })
	file, err := loader.Load(t.Context(), "compose.yaml")
	require.NoError(t, err)
	require.Len(t, file.Compose.Services, 2)
	assert.Equal(t, "z", file.Compose.Services[0].Name)
	assert.EqualValues(t, 3, *file.Compose.Services[0].Deploy.Replicas)
	assert.Equal(t, "3", file.Compose.Services[0].Environment.Map["replicas"])
	assert.Equal(t, "a", file.Compose.Services[1].Name)
	assert.EqualValues(t, 4, *file.Compose.Services[1].Deploy.Replicas)
}
