package faults

import "fmt"

// Code identifies a typed diagnostic cause.
type Code string

const (
	// CodeImageNotFound indicates that Docker reported a missing image.
	CodeImageNotFound Code = "image_not_found"
	// CodeExternalNetworkNotFound indicates that a declared external network is missing.
	CodeExternalNetworkNotFound Code = "external_network_not_found"
)

// ExternalNetworkNotFoundError reports a missing network declared external in Compose.
type ExternalNetworkNotFoundError struct {
	// Name is the resolved Docker network name.
	Name string
}

func (e *ExternalNetworkNotFoundError) Error() string {
	return fmt.Sprintf("external network %s does not exist", e.Name)
}
