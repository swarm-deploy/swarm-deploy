package model

import (
	"time"
)

type (
	Severity string
	Type     string
)

const (
	SeverityLow    Severity = "low"
	SeverityMedium Severity = "medium"
	SeverityHigh   Severity = "high"

	TypeServiceResourcesUnspecified       Type = "service.resources.unspecified"
	TypeServiceResourcesLimitsUnspecified Type = "service.resources.limits.unspecified"
	TypeServiceImageLatest                Type = "service.image.latest"
	TypeServiceImageDigestUnspecified     Type = "service.image.digest.unspecified"
)

type Recommendation struct {
	Severity Severity `json:"severity"`
	Type     Type     `json:"type"`
	Source   Source   `json:"source"`
	Subject  Subject  `json:"subject"`

	Recommendation string `json:"recommendation"`

	CreatedAt time.Time `json:"created_at"`
}

type Subject struct {
	Stack   string `json:"stack"`
	Service string `json:"service"`
}

type Source struct {
	File   string `json:"file"`
	Digest string `json:"digest"`
	Commit string `json:"commit"`
}

func SourceFromStack(stack Stack) Source {
	return Source{
		File:   stack.Definition.Path,
		Digest: stack.Definition.Digest,
		Commit: stack.Commit,
	}
}

func (r *Recommendation) ID() string {
	return r.Subject.Stack + "-" + r.Subject.Service + "-" + r.Source.File + string(r.Type)
}
