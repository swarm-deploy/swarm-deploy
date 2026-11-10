package faults

// Code identifies a typed diagnostic cause.
type Code string

// CodeImageNotFound indicates that Docker reported a missing image.
const CodeImageNotFound Code = "image_not_found"

// CodeImageExtractionFailed indicates that Docker could not extract an image filesystem.
const CodeImageExtractionFailed Code = "image_extraction_failed"

// CodeNoSuitableNode indicates that Swarm could not schedule a task.
const CodeNoSuitableNode Code = "no_suitable_node"

// CodeImagePullDenied indicates that a registry explicitly denied image access.
const CodeImagePullDenied Code = "image_pull_denied"

// CodeMountFailed indicates that Docker could not create or mount a volume or bind mount.
const CodeMountFailed Code = "mount_failed"

// CodeInvalidDesiredState indicates that Compose validation failed.
const CodeInvalidDesiredState Code = "invalid_desired_state"

// Coded is an error with a stable diagnostic code
// and a concise description of the cause.
type Coded interface {
	error

	// FaultCode identifies the diagnostic cause.
	FaultCode() Code

	// FaultSummary describes the specific cause.
	FaultSummary() string
}
