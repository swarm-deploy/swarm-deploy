package initjob

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/pkg/stdcopy"
)

func (r *InitJobRunner) loadServiceLogs(
	ctx context.Context,
	serviceID string,
	serviceName string,
) ([]byte, bool, error) {
	logsCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), initJobLogCollectionTimeout)
	defer cancel()
	reader, err := r.dockerClient.ServiceLogs(logsCtx, serviceID, container.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Tail:       "all",
	})
	if err != nil {
		return nil, false, fmt.Errorf("read logs for service %s: %w", serviceName, err)
	}
	defer reader.Close()

	tail := newTailBuffer(initJobLogLimit)
	if _, err = stdcopy.StdCopy(tail, tail, reader); err != nil {
		return nil, false, fmt.Errorf("read logs stream for service %s: %w", serviceName, err)
	}
	return tail.Bytes(), tail.Truncated(), nil
}

func initJobExecutionStatus(err error) Status {
	if err == nil {
		return Succeeded
	}
	var failed *JobFailedError
	if errors.As(err, &failed) {
		return Failed
	}
	return Interrupted
}

func initJobDiagnosticLogLines(logs []byte) []string {
	text := strings.TrimSpace(string(logs))
	if text == "" {
		return []string{}
	}
	return strings.Split(text, "\n")
}

type tailBuffer struct {
	limit int
	data  []byte
	total int64
}

func newTailBuffer(limit int) *tailBuffer {
	return &tailBuffer{limit: limit, data: make([]byte, 0, limit)}
}

func (b *tailBuffer) Write(p []byte) (int, error) {
	written := len(p)
	b.total += int64(written)
	if b.limit <= 0 || written == 0 {
		return written, nil
	}
	if written >= b.limit {
		b.data = append(b.data[:0], p[written-b.limit:]...)
		return written, nil
	}
	overflow := len(b.data) + written - b.limit
	if overflow > 0 {
		copy(b.data, b.data[overflow:])
		b.data = b.data[:len(b.data)-overflow]
	}
	b.data = append(b.data, p...)
	return written, nil
}

func (b *tailBuffer) Bytes() []byte {
	if len(b.data) == 0 {
		return []byte{}
	}
	return append([]byte(nil), b.data...)
}

func (b *tailBuffer) Truncated() bool { return b.total > int64(b.limit) }

var _ io.Writer = (*tailBuffer)(nil)
