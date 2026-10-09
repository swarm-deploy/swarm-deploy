// Package deployment owns apply attempts and successful effective desired baselines.
package deployment

import (
	"time"

	"github.com/swarm-deploy/swarm-deploy/internal/compose"
)

// Status describes an apply attempt, not Swarm service health.
type Status string

const (
	// Running is committed before any external apply action.
	Running Status = "running"
	// Succeeded means apply returned successfully; it does not promise service convergence.
	Succeeded Status = "succeeded"
	// Failed means an apply action failed.
	Failed Status = "failed"
	// Interrupted means the process stopped before recording an apply outcome.
	Interrupted Status = "interrupted"
)

// Change describes a semantic value transition without exposing secrets.
type Change struct {
	// Path identifies the effective Compose field or referenced resource.
	Path string `json:"path"`
	// Before is the old safe value, absent when newly added.
	Before *string `json:"before,omitempty"`
	// After is the new safe value, absent when removed.
	After *string `json:"after,omitempty"`
	// Redacted indicates that a sensitive value changed without disclosing it.
	Redacted bool `json:"redacted,omitempty"`
}

// Deployment is one actual attempt to apply changed effective desired state.
type Deployment struct {
	// ID uniquely identifies this attempt; retries receive new IDs.
	ID string `json:"id"`
	// Stack identifies the deployment target.
	Stack string `json:"stack"`
	// Commit identifies the Git revision being applied.
	Commit string `json:"commit"`
	// Status is the durable apply outcome.
	Status Status `json:"status"`
	// StartedAt is recorded before external apply.
	StartedAt time.Time `json:"started_at"`
	// FinishedAt is absent while running.
	FinishedAt *time.Time `json:"finished_at,omitempty"`
	// ErrorCode is a safe diagnostic category, never raw errors or logs.
	ErrorCode string `json:"error_code,omitempty"`
	// Changes are relative to the last successful baseline.
	Changes []Change `json:"changes"`
}

// Prepared contains the redacted effective Compose and private comparison fingerprints.
// It is persisted in the deployment repository, never in an event envelope.
type Prepared struct {
	// Definition retains supported effective Compose values after masking.
	Definition compose.File `json:"definition"`
	// Digest identifies the unmasked effective state without retaining plaintext.
	Digest string `json:"digest"`
	// Fields contains safe display values indexed by semantic path.
	Fields map[string]string `json:"fields"`
	// Fingerprints distinguish changes to values which have identical masked displays.
	Fingerprints map[string]string `json:"fingerprints"`
	// Redacted marks paths whose values have been masked.
	Redacted map[string]bool `json:"redacted"`
}

// ListFilter selects recent attempts.
type ListFilter struct {
	// Stack restricts results to a stack; empty means all stacks.
	Stack string
	// Limit bounds returned attempts; values outside 1..100 use 20.
	Limit int
}
