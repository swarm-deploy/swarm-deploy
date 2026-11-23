package faults

import (
	"context"
	"errors"
	"net"
	"net/http"
)

func HTTPCodeKind(code int) string {
	switch code {
	case http.StatusBadRequest:
		return "bad_request"
	case http.StatusUnauthorized:
		return "unauthorized"
	case http.StatusForbidden:
		return "forbidden"
	case http.StatusNotFound:
		return "not_found"
	case http.StatusConflict:
		return "conflict"
	case http.StatusTooManyRequests:
		return "rate_limited"
	default:
		if code >= http.StatusInternalServerError {
			return "server_error"
		}
		if code >= http.StatusBadRequest {
			return "client_error"
		}
		return "unexpected_response"
	}
}

func TransportErrorKind(err error) string {
	if errors.Is(err, context.Canceled) {
		return "canceled"
	}

	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}

	if _, ok := errors.AsType[*net.DNSError](err); ok {
		return "dns_error"
	}

	if netErr, ok := errors.AsType[net.Error](err); ok && netErr.Timeout() {
		return "timeout"
	}

	return "connection_failed"
}
