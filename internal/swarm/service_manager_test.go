package swarm

import (
	"bytes"
	"context"
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

func TestBuildDockerTaskLogsOptionsDefaults(t *testing.T) {
	options := buildDockerTaskLogsOptions(TaskLogsOptions{})

	assert.Equal(t, container.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Timestamps: true,
		Follow:     false,
		Tail:       "200",
	}, options, "unexpected default task logs options")
}

func TestBuildDockerTaskLogsOptionsWithFollowAndTail(t *testing.T) {
	options := buildDockerTaskLogsOptions(TaskLogsOptions{
		Follow: true,
		Limit:  25,
	})

	assert.Equal(t, "25", options.Tail, "unexpected tail")
	assert.True(t, options.Follow, "follow must be enabled")
	assert.True(t, options.ShowStdout, "stdout must be enabled")
	assert.True(t, options.ShowStderr, "stderr must be enabled")
	assert.True(t, options.Timestamps, "timestamps must be enabled")
}

func TestReadDockerLogEntriesDemultiplexesStreamAndTimestamp(t *testing.T) {
	raw := append(
		encodeDockerLogFrame(1, []byte("2026-09-15T18:20:11.123Z server started\n")),
		encodeDockerLogFrame(2, []byte("2026-09-15T18:20:12.456Z failed request\n"))...,
	)
	entries := make(chan LogEntry, 2)

	err := readDockerLogEntries(context.Background(), bytes.NewReader(raw), entries)
	close(entries)
	require.NoError(t, err, "read docker log entries")

	got := make([]LogEntry, 0, 2)
	for entry := range entries {
		got = append(got, entry)
	}

	require.Len(t, got, 2)
	assert.Equal(t, defaultLogStream, got[0].Stream)
	assert.Equal(t, "server started", got[0].Message)
	assert.Equal(t, time.Date(2026, time.September, 15, 18, 20, 11, 123000000, time.UTC), got[0].Timestamp)
	assert.Equal(t, stderrLogStream, got[1].Stream)
	assert.Equal(t, "failed request", got[1].Message)
}

func TestReadDockerLogEntriesKeepsShortPlainText(t *testing.T) {
	entries := make(chan LogEntry, 1)

	err := readDockerLogEntries(context.Background(), bytes.NewReader([]byte("ready\n")), entries)
	close(entries)
	require.NoError(t, err, "read docker log entries")

	got := make([]LogEntry, 0, 1)
	for entry := range entries {
		got = append(got, entry)
	}

	require.Len(t, got, 1)
	assert.Equal(t, defaultLogStream, got[0].Stream)
	assert.Equal(t, "ready", got[0].Message)
}

func TestToServiceConfigRefsMapsReferencesWithoutPayload(t *testing.T) {
	refs := toServiceConfigRefs([]*dockerswarm.ConfigReference{
		{
			ConfigID:   "cfg-id",
			ConfigName: "prod_pomerium_config",
			File: &dockerswarm.ConfigReferenceFileTarget{
				Name: "/etc/pomerium/config.yaml",
			},
		},
	})

	assert.Equal(t, []ServiceConfig{
		{
			ConfigID:   "cfg-id",
			ConfigName: "prod_pomerium_config",
			Target:     "/etc/pomerium/config.yaml",
		},
	}, refs)
	assert.Empty(t, refs[0].Data, "service status must not load config payload")
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

func TestStackServiceNameFromFullName(t *testing.T) {
	assert.Equal(
		t,
		"api",
		stackServiceNameFromFullName("payments", "payments_api"),
		"stack prefix must be removed",
	)
	assert.Equal(
		t,
		"foreign_api",
		stackServiceNameFromFullName("payments", "foreign_api"),
		"foreign service names must stay unchanged",
	)
}

func encodeDockerLogFrame(stream byte, payload []byte) []byte {
	frame := make([]byte, dockerLogFrameHeaderSize+len(payload))
	frame[0] = stream
	binary.BigEndian.PutUint32(frame[4:dockerLogFrameHeaderSize], uint32(len(payload)))
	copy(frame[dockerLogFrameHeaderSize:], payload)

	return frame
}
