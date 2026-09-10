package tracing

import "go.opentelemetry.io/otel/attribute"

var (
	ResourceStackName = attribute.Key("swarm-deploy.resource.stack_name")
	SyncCommitSha     = attribute.Key("swarm-deploy.sync.commit_sha")
)
