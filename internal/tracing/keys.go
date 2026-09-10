package tracing

import "go.opentelemetry.io/otel/attribute"

var (
	ResourceStackName   = attribute.Key("swarm-deploy.resource.stack.name")
	ResourceComposePath = attribute.Key("swarm-deploy.resource.compose.path")

	SyncCommitSha = attribute.Key("swarm-deploy.sync.commit_sha")
)
