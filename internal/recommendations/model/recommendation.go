package model

import "time"

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
)

type Recommendation struct {
	Severity Severity `json:"severity"`
	Type     Type     `json:"type"`
	Commit   string `json:"commit"`
	Subject  Subject `json:"subject"`

	Recommendation string `json:"recommendation"`

	CreatedAt time.Time `json:"created_at"`
}

type Subject struct {
	Stack   string `json:"stack"`
	Service string `json:"service"`
}

func (r *Recommendation) ID() string {
	return r.Subject.Stack + "-" + r.Subject.Service + "-" + string(r.Type) + string(r.Severity)
}

