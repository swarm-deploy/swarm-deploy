package tracing

import "go.opentelemetry.io/otel/attribute"

var (
	ResourceStackName = attribute.Key("swarm-deploy.resource.stack.name")

	ResourceComposePath   = attribute.Key("swarm-deploy.resource.compose.path")
	ResourceComposeDigest = attribute.Key("swarm-deploy.resource.compose.digest")

	ResourceServiceName     = attribute.Key("swarm-deploy.resource.service.name")
	ResourceServiceID       = attribute.Key("swarm-deploy.resource.service.id")
	ResourceServiceReplicas = attribute.Key("swarm-deploy.resource.service.replicas")

	ResourceNetworkName = attribute.Key("swarm-deploy.resource.network.name")
	ResourceNetworkID   = attribute.Key("swarm-deploy.resource.network.id")
	ResourceConfigName  = attribute.Key("swarm-deploy.resource.config.name")
	ResourceConfigID    = attribute.Key("swarm-deploy.resource.config.id")

	SyncCommitSha = attribute.Key("swarm-deploy.sync.commit_sha")

	EventName           = attribute.Key("swarm-deploy.events.event.name")
	EventSubscriberName = attribute.Key("swarm-deploy.events.subscriber.name")
	EventQueueName      = attribute.Key("swarm-deploy.events.queue.name")

	ServiceVersion   = attribute.Key("service.version")
	ServiceBuildTime = attribute.Key("service.build.time")

	GenAIToolName           = attribute.Key("gen_ai.tool.name")
	GenAIToolType           = attribute.Key("gen_ai.tool.type")
	GenAIToolDescription           = attribute.Key("gen_ai.tool.description")
	GenAIToolCallArguments  = attribute.Key("gen_ai.tool.call.arguments")
	GenAIToolCallResult     = attribute.Key("gen_ai.tool.call.result")
	GenAIConversationID     = attribute.Key("gen_ai.conversation.id")
	GenAIRequestModel       = attribute.Key("gen_ai.request.model")
	GenAIUsageInputTokens   = attribute.Key("gen_ai.usage.input_tokens")
	GenAIUsageOutputTokens  = attribute.Key("gen_ai.usage.output_tokens")
)
