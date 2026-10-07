package httpx

// Unmarshaler decodes an HTTP response body into out.
type Unmarshaler func(data []byte, out any) error
