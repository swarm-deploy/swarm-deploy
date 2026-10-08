// Package faults provides common classification of technical failures.
package faults

// Error is an error with a technical classification.
type Error interface {
	error

	// Temporary reports whether the failure may disappear on retry.
	Temporary() bool
}
