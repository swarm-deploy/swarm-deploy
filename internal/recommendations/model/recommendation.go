package model

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

	Subject struct {
		Stack   string `json:"stack"`
		Service string `yaml:"service"`
	} `json:"subject"`

	Recommendation string `json:"recommendation"`
}
