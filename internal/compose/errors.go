package compose

import (
	"fmt"
	"strings"
)

// ReadComposeError means a compose file could not be read.
type ReadComposeError struct {
	// FilePath is the compose file path.
	FilePath string
	// Err is the underlying (technical) cause.
	Err error
}

func (e *ReadComposeError) Error() string {
	return fmt.Sprintf("read compose file %q: %v", e.FilePath, e.Err)
}

// Unwrap returns the cause.
func (e *ReadComposeError) Unwrap() error { return e.Err }

// ParseComposeError means a compose file could not be parsed.
type ParseComposeError struct {
	// FilePath is the compose file path.
	FilePath string
	// Err is the underlying cause.
	Err error
}

func (e *ParseComposeError) Error() string {
	return fmt.Sprintf("parse compose file %q: %v", e.FilePath, e.Err)
}

// Unwrap returns the cause.
func (e *ParseComposeError) Unwrap() error { return e.Err }

// Validation issue codes.
const (
	// IssueCodeRequired means a required field is missing.
	IssueCodeRequired = "required"
	// IssueCodeInvalidType means a value has an unexpected YAML type.
	IssueCodeInvalidType = "invalid_type"
	// IssueCodeInvalidValue means a value is not acceptable for the compose specification.
	IssueCodeInvalidValue = "invalid_value"
)

// ValidationIssue describes a single compose validation problem.
type ValidationIssue struct {
	// ResourceType is the type of the invalid resource (service, network, ...).
	ResourceType string
	// ResourceName is the name of the invalid resource.
	ResourceName string
	// Field is the invalid field.
	Field string
	// Code is a machine-readable issue code.
	Code string
	// Message is a human-readable description.
	Message string
}

// ValidateComposeError means a compose file is invalid.
type ValidateComposeError struct {
	// FilePath is the compose file path.
	FilePath string
	// Issues are found validation issues.
	Issues []ValidationIssue
	// Err is the optional underlying decoding error.
	Err error
}

func (e *ValidateComposeError) Error() string {
	msgs := make([]string, 0, len(e.Issues))
	for _, issue := range e.Issues {
		msgs = append(msgs, issue.String())
	}
	return fmt.Sprintf("validate compose file %q: %s", e.FilePath, strings.Join(msgs, "; "))
}

// Unwrap returns the underlying decoding error, if any.
func (e *ValidateComposeError) Unwrap() error { return e.Err }

// String formats the issue omitting empty attributes.
func (i ValidationIssue) String() string {
	resource := i.ResourceType
	if i.ResourceName != "" {
		resource = fmt.Sprintf("%s %q", resource, i.ResourceName)
	}

	parts := make([]string, 0, 3)
	for _, part := range []string{resource, i.Field, i.Message} {
		if part != "" {
			parts = append(parts, part)
		}
	}

	return strings.Join(parts, ": ")
}
