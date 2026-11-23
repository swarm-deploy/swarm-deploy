package imageupdate

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUpdateComposeImage(t *testing.T) {
	const original = `x-common: &common
  stop_grace_period: 30s
services:
  api:
    <<: *common
    image: "localhost:5000/team/api:1.0" # keep this comment
  worker:
    image: localhost:5000/team/api:1.0
`
	updated, image, err := updateComposeImage([]byte(original), "api", "2.0")
	require.NoError(t, err)
	assert.Equal(t, "localhost:5000/team/api:2.0", image)
	assert.Contains(t, string(updated), `image: "localhost:5000/team/api:2.0" # keep this comment`)
	assert.Contains(t, string(updated), "image: localhost:5000/team/api:1.0")
	assert.Contains(t, string(updated), "&common")
	_, _, err = updateComposeImage([]byte(original), "api", "1.0")
	require.ErrorContains(t, err, "already uses")
}
