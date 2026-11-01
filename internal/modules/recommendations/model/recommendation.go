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

	TypeServiceResourcesUnspecified           Type = "service.resources.unspecified"
	TypeServiceResourcesLimitsUnspecified     Type = "service.resources.limits.unspecified"
	TypeServiceImageLatest                    Type = "service.image.latest"
	TypeServiceImageDigestUnspecified         Type = "service.image.digest.unspecified"
	TypeServiceRestartPolicyUnspecified       Type = "service.restart-policy.unspecified"
	TypeServiceSelfHealScheduledService       Type = "service.self-heal.scheduled-service"
	TypeServiceDockerSocketMount              Type = "service.docker-socket.mount"
	TypeServiceCapabilitiesAll                Type = "service.capabilities.all"
	TypeServiceCapabilitiesPrivileged         Type = "service.capabilities.privileged"
	TypeServiceCapabilitiesSensitive          Type = "service.capabilities.sensitive"
	TypeServiceHostPortStartFirst             Type = "service.host-port.start-first"
	TypeServiceSwarmDeployDockerConfigMissing Type = "service.swarm-deploy.docker-config.missing"
)

type Recommendation struct {
	Severity Severity `json:"severity" db:"severity"`
	Type     Type     `json:"type" db:"type"`
	Source   Source   `json:"source" db:"source"`
	Subject  Subject  `json:"subject" db:"subject"`

	Title          string `json:"title" db:"title"`
	Recommendation string `json:"recommendation" db:"recommendation"`

	CreatedAt time.Time `json:"created_at" db:"created_at"`
}

type Subject struct {
	Stack   string `json:"stack" db:"stack"`
	Service string `json:"service" db:"service"`
}

type Source struct {
	File   string `json:"file" db:"file"`
	Digest string `json:"digest" db:"digest"`
	Commit string `json:"commit" db:"commit"`
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
