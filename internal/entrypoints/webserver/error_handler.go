package webserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/ogen-go/ogen/ogenerrors"
)

type statusError struct {
	code int
	err  error
}

func (e *statusError) Error() string {
	return e.err.Error()
}

func (e *statusError) Unwrap() error {
	return e.err
}

func withStatusError(code int, err error) error {
	return &statusError{
		code: code,
		err:  err,
	}
}

func handleHTTPError(_ context.Context, w http.ResponseWriter, _ *http.Request, err error) {
	code := ogenerrors.ErrorCode(err)
	var sErr *statusError
	if errors.As(err, &sErr) {
		code = sErr.code
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"error_message": err.Error(),
	})
}
