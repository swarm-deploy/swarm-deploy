package authenticator

import "net/http"

type Request struct {
	Request *http.Request
	Body    []byte
}
