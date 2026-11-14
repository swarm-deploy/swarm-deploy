package swarm

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"syscall"
	"testing"

	cerrdefs "github.com/containerd/errdefs"
	dockerswarm "github.com/docker/docker/api/types/swarm"
	"github.com/docker/docker/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/faults"
)

func TestClassifyDockerError(t *testing.T) {
	cause := &net.OpError{Op: "request", Net: "docker", Err: cerrdefs.ErrUnavailable}
	classified := classifyDockerError(cause)
	var unavailable *DockerUnavailableError
	require.ErrorAs(t, classified, &unavailable)
	assert.Same(t, cause, unavailable.Unwrap())
	assert.EqualError(t, classified, cause.Error())
	assert.ErrorIs(t, classified, cerrdefs.ErrUnavailable)
	var original *net.OpError
	require.ErrorAs(t, classified, &original)
	assert.Same(t, cause, original)
	var temporary faults.Error
	require.ErrorAs(t, classified, &temporary)
	assert.True(t, temporary.Temporary())

	for _, tc := range []struct {
		name string
		err  error
	}{
		{name: "nil"},
		{name: "permanent", err: cerrdefs.ErrInvalidArgument},
		{name: "canceled", err: fmt.Errorf("request: %w", context.Canceled)},
		{name: "deadline", err: fmt.Errorf("request: %w", context.DeadlineExceeded)},
		{name: "unavailable with cancellation", err: errors.Join(cerrdefs.ErrUnavailable, context.Canceled)},
		{name: "unavailable with deadline", err: errors.Join(cerrdefs.ErrUnavailable, context.DeadlineExceeded)},
		{name: "already classified", err: classified},
		{name: "context around classified", err: fmt.Errorf("apply service: %w", classified)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.err, classifyDockerError(tc.err))
		})
	}
}

func TestDockerCreateConflictClassification(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"conflict", cerrdefs.ErrConflict, true},
		{"already exists", fmt.Errorf("Docker create: %w", cerrdefs.ErrAlreadyExists), true},
		{"unavailable", cerrdefs.ErrUnavailable, false},
		{"invalid", cerrdefs.ErrInvalidArgument, false},
	} {
		t.Run(tc.name, func(t *testing.T) { assert.Equal(t, tc.want, isConflictErr(tc.err)) })
	}
}

func TestClassifyDockerTransportErrors(t *testing.T) {
	for _, tc := range []struct {
		name      string
		cause     error
		temporary bool
	}{
		{"refused", &net.OpError{Op: "dial", Err: syscall.ECONNREFUSED}, true},
		{"reset", &net.OpError{Op: "read", Err: syscall.ECONNRESET}, true},
		{"aborted", &net.OpError{Op: "read", Err: syscall.ECONNABORTED}, true},
		{"broken pipe", &net.OpError{Op: "write", Err: syscall.EPIPE}, true},
		{"temporary DNS", &net.DNSError{IsTemporary: true}, true},
		{"missing host", &net.DNSError{IsNotFound: true}, false},
		{"invalid address", &net.AddrError{Err: "invalid endpoint"}, false},
		{"socket permission", &net.OpError{Op: "dial", Err: syscall.EACCES}, false},
		{"invalid configuration", &net.OpError{Op: "dial", Err: syscall.EINVAL}, false},
		{"TLS verification", &tls.CertificateVerificationError{Err: x509.UnknownAuthorityError{}}, false},
		{"TLS protocol", tls.RecordHeaderError{}, false},
		{"unstructured text", errors.New("connection refused"), false},
		{"truncated rejection", fmt.Errorf("%w: %w", cerrdefs.ErrPermissionDenied, io.ErrUnexpectedEOF), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cause := fmt.Errorf("Docker request: %w", tc.cause)
			classified := classifyDockerError(cause)
			var unavailable *DockerUnavailableError
			assert.Equal(t, tc.temporary, errors.As(classified, &unavailable))
			assert.ErrorIs(t, classified, tc.cause)
			if tc.temporary {
				require.NotNil(t, unavailable)
				assert.Same(t, cause, unavailable.Unwrap())
				assert.True(t, unavailable.Temporary())
				assert.Same(t, classified, classifyDockerError(classified))
			} else {
				assert.Same(t, cause, classified)
			}
		})
	}
}

func TestDockerSDKConnectionFailuresAreTemporary(t *testing.T) {
	for _, tc := range []struct {
		name  string
		cause error
	}{
		{"refused", nil}, // The SDK discards ECONNREFUSED; retain its connection-failed error.
		{"dropped", io.EOF},
		{"reset", syscall.ECONNRESET},
		{"truncated", io.ErrUnexpectedEOF},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				_, readErr := io.Copy(io.Discard, req.Body)
				require.NoError(t, readErr)
				conn, rw, err := http.NewResponseController(w).Hijack()
				require.NoError(t, err)
				defer conn.Close()
				if tc.name == "reset" {
					tcp, ok := conn.(*net.TCPConn)
					require.True(t, ok)
					require.NoError(t, tcp.SetLinger(0))
				}
				if tc.name == "truncated" {
					_, err = rw.WriteString("HTTP/1.1 201 Created\r\nContent-Type: application/json\r\nContent-Length: 100\r\n\r\n{\"ID\":")
					require.NoError(t, err)
					require.NoError(t, rw.Flush())
				}
			}))
			t.Cleanup(server.Close)
			cli := newTestDockerClient(t, server)
			t.Cleanup(func() { require.NoError(t, cli.Close()) })
			if tc.name == "refused" {
				server.Close()
			}
			_, cause := cli.ServiceCreate(t.Context(), dockerswarm.ServiceSpec{}, dockerswarm.ServiceCreateOptions{})
			require.Error(t, cause)
			if tc.cause != nil {
				assert.ErrorIs(t, cause, tc.cause)
			} else {
				assert.True(t, client.IsErrConnectionFailed(cause))
			}
			classified := classifyDockerError(cause)
			var unavailable *DockerUnavailableError
			require.ErrorAs(t, classified, &unavailable)
			assert.True(t, unavailable.Temporary())
			assert.Equal(t, cause, unavailable.Unwrap())
			assert.ErrorIs(t, classified, cause)
			var requestError *url.Error
			if errors.As(cause, &requestError) {
				var preserved *url.Error
				require.ErrorAs(t, classified, &preserved)
				assert.Same(t, requestError, preserved)
			}
		})
	}
}

func TestDockerSDKPermanentConnectionFailures(t *testing.T) {
	for _, tc := range []struct {
		name      string
		tlsServer bool
		scheme    string
	}{
		{"untrusted certificate", true, "https"},
		{"HTTPS against HTTP", false, "https"},
		{"HTTP against HTTPS", true, "http"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"ID":"id"}`))
			}))
			if tc.tlsServer {
				server.StartTLS()
			} else {
				server.Start()
			}
			t.Cleanup(server.Close)
			cli, err := client.NewClientWithOpts(
				client.WithHost(server.URL), client.WithScheme(tc.scheme), client.WithVersion("1.48"),
			)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, cli.Close()) })
			_, cause := cli.ServiceCreate(t.Context(), dockerswarm.ServiceSpec{}, dockerswarm.ServiceCreateOptions{})
			require.Error(t, cause)
			classified := classifyDockerError(cause)
			assert.Equal(t, cause, classified)
			assert.ErrorIs(t, classified, cause)
			var unavailable *DockerUnavailableError
			assert.NotErrorAs(t, classified, &unavailable)
			var temporary faults.Error
			assert.False(t, errors.As(classified, &temporary) && temporary.Temporary())
		})
	}
}
