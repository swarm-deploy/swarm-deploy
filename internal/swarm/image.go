package swarm

import "errors"

// ErrImageNotFound means that image does not exist in Docker.
var ErrImageNotFound = errors.New("image not found")

// Image contains compact Docker image metadata.
type Image struct {
	// ID is a Docker image identifier.
	ID string `json:"id"`
	// Ref is a requested image reference.
	Ref string `json:"ref"`
	// Labels contains OCI image config labels.
	Labels map[string]string `json:"labels,omitempty"`
}
