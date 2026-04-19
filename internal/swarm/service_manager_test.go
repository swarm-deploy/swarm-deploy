package swarm

import (
	"encoding/binary"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
	dockerswarm "github.com/docker/docker/api/types/swarm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDemultiplexDockerLogStreamReturnsRawOnPlainText(t *testing.T) {
	raw := []byte("2026-04-18T12:00:00Z hello\n2026-04-18T12:00:01Z warning\n")

	decoded := demultiplexDockerLogStream(raw)

	assert.Equal(t, raw, decoded, "plain-text stream must stay unchanged")
}

func TestDemultiplexDockerLogStreamDemultiplexesFrames(t *testing.T) {
	frame1 := []byte("2026-04-18T12:00:00Z stdout hello\n")
	frame2 := []byte("2026-04-18T12:00:01Z stderr warning\n")

	raw := append(encodeDockerLogFrame(1, frame1), encodeDockerLogFrame(2, frame2)...)
	expected := append([]byte{}, frame1...)
	expected = append(expected, frame2...)

	decoded := demultiplexDockerLogStream(raw)

	assert.Equal(t, expected, decoded, "multiplexed stream must be demultiplexed")
}

func TestBuildDockerServiceLogsOptionsDefaults(t *testing.T) {
	options := buildDockerServiceLogsOptions(ServiceLogsOptions{})

	assert.Equal(t, container.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Timestamps: true,
		Tail:       "200",
	}, options, "unexpected default logs options")
}

func TestBuildDockerServiceLogsOptionsWithBounds(t *testing.T) {
	since := time.Date(2026, time.April, 18, 9, 0, 0, 123000000, time.FixedZone("UTC+3", 3*60*60))
	until := since.Add(5 * time.Minute)

	options := buildDockerServiceLogsOptions(ServiceLogsOptions{
		Limit: 123,
		Since: &since,
		Until: &until,
	})

	require.Equal(t, "123", options.Tail, "unexpected tail")
	assert.Equal(t, "2026-04-18T06:00:00.123Z", options.Since, "unexpected since")
	assert.Equal(t, "2026-04-18T06:05:00.123Z", options.Until, "unexpected until")
	assert.True(t, options.ShowStdout, "stdout must be enabled")
	assert.True(t, options.ShowStderr, "stderr must be enabled")
	assert.True(t, options.Timestamps, "timestamps must be enabled")
}

func TestResolveServiceDeployModeReplicated(t *testing.T) {
	replicas := uint64(4)

	mode, count := resolveServiceDeployMode(dockerswarm.ServiceMode{
		Replicated: &dockerswarm.ReplicatedService{
			Replicas: &replicas,
		},
	})

	assert.Equal(t, "replicated", mode, "unexpected deploy mode")
	assert.Equal(t, uint64(4), count, "unexpected replicas count")
}

func TestResolveServiceDeployModeGlobal(t *testing.T) {
	mode, count := resolveServiceDeployMode(dockerswarm.ServiceMode{
		Global: &dockerswarm.GlobalService{},
	})

	assert.Equal(t, "global", mode, "unexpected deploy mode")
	assert.Equal(t, uint64(0), count, "global mode must not report replicas")
}

func encodeDockerLogFrame(stream byte, payload []byte) []byte {
	frame := make([]byte, dockerLogFrameHeaderSize+len(payload))
	frame[0] = stream
	binary.BigEndian.PutUint32(frame[4:dockerLogFrameHeaderSize], uint32(len(payload)))
	copy(frame[dockerLogFrameHeaderSize:], payload)

	return frame
}
