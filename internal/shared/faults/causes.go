package faults

import "errors"

// Cause is one typed failure and its own diagnostic classification.
type Cause struct {
	// Code identifies the type of failure.
	Code Code `json:"code"`
	// Summary contains a bounded, secret-masked description.
	Summary string `json:"summary"`
	// Temporary reports whether this cause may disappear on retry.
	Temporary bool `json:"temporary"`
}

// Causes returns the typed causes in error-tree order, with masked summaries.
func Causes(err error) []Cause {
	return appendCauses(nil, err)
}

func appendCauses(causes []Cause, err error) []Cause {
	var classification Error
	for err != nil {
		if safe, ok := err.(safeCausesError); ok {
			return append(causes, safe.causes...)
		}
		if classification == nil {
			classification, _ = err.(Error)
		}
		if coded, ok := err.(Coded); ok && coded.FaultCode() != "" {
			if classification == nil {
				classification = branchClassification(errors.Unwrap(err))
			}
			return append(causes, Cause{
				Code: coded.FaultCode(), Summary: sanitizeDiagnostic(coded.FaultSummary()),
				Temporary: classification != nil && classification.Temporary(),
			})
		}
		if joined, ok := err.(interface{ Unwrap() []error }); ok {
			for _, child := range joined.Unwrap() {
				causes = appendCauses(causes, child)
			}
			return causes
		}
		err = errors.Unwrap(err)
	}
	return causes
}

// branchClassification stops at a join so a sibling's classification cannot
// be attributed to a coded error wrapping the whole join.
func branchClassification(err error) Error {
	for ; err != nil; err = errors.Unwrap(err) {
		if classified, ok := err.(Error); ok {
			return classified
		}
	}
	return nil
}
