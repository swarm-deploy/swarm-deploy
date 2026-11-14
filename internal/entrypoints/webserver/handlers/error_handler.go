package handlers

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
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

func HandleHTTPError(_ context.Context, w http.ResponseWriter, _ *http.Request, err error) {
	code := ogenerrors.ErrorCode(err)
	var sErr *statusError
	if errors.As(err, &sErr) {
		code = sErr.code
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.MarshalEncode(jsontext.NewEncoder(w, jsontext.EscapeForHTML(true), jsontext.EscapeForJS(true),
		jsontext.AllowInvalidUTF8(true)), map[string]string{
		"error_message": err.Error(),
	})
}
