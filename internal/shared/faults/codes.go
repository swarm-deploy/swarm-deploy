package faults

// Code identifies a typed diagnostic cause.
type Code string

// CodeImageNotFound indicates that Docker reported a missing image.
const CodeImageNotFound Code = "image_not_found"

// Coded is an error with a stable diagnostic code.
type Coded interface {
	error

	// FaultCode identifies the diagnostic cause.
	FaultCode() Code
}
