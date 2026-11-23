package swarm

import (
	"strings"

	"github.com/distribution/reference"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/faults"
)

// ErrImageNotFound means that image does not exist in Docker.
var ErrImageNotFound = imageNotFoundError{}

type imageNotFoundError struct{}

func (imageNotFoundError) Error() string { return "image not found" }

func (imageNotFoundError) FaultCode() faults.Code { return faults.CodeImageNotFound }

func (e imageNotFoundError) FaultSummary() string { return e.Error() }

// ImageNotFound reports whether Docker rejected this task with a missing-image diagnostic.
func (t ServiceTask) ImageNotFound() bool {
	if t.CurrentState != TaskStateRejected || t.Spec.value.ContainerSpec == nil {
		return false
	}
	image := t.Spec.value.ContainerSpec.Image
	// Older engines report that the image is absent from the worker. They may
	// omit the underlying reason the pull failed.
	if image != "" && t.Error == "No such image: "+image {
		return true
	}
	ref, err := reference.ParseNormalizedNamed(image)
	if err != nil {
		return false
	}
	ref = reference.TagNameOnly(ref)
	fullName, shortName := ref.String(), reference.FamiliarString(ref)
	manifestUnknown := "manifest for " + shortName + " not found: manifest unknown"
	return t.Error == fullName+": not found" ||
		t.Error == "failed to resolve reference \""+fullName+"\": "+fullName+": not found" ||
		t.Error == manifestUnknown || strings.HasPrefix(t.Error, manifestUnknown+": ")
}

// Image contains compact Docker image metadata.
type Image struct {
	// ID is a Docker image identifier.
	ID string `json:"id"`
	// Ref is a requested image reference.
	Ref string `json:"ref"`
	// Labels contains OCI image config labels.
	Labels map[string]string `json:"labels,omitempty"`
}
