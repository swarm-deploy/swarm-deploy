package swarm

import (
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestTailBufferKeepsExactLastBytes(t *testing.T) {
	buffer := newTailBuffer(4)
	_, err := buffer.Write([]byte{0, 1, 2})
	require.NoError(t, err)
	_, err = buffer.Write([]byte{3, 4, 5})
	require.NoError(t, err)
	assert.Equal(t, []byte{2, 3, 4, 5}, buffer.Bytes())
	assert.True(t, buffer.Truncated())
}
