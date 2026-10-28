package tracing

import "go.opentelemetry.io/otel/attribute"

var (
	SyncReason = attribute.Key("swarm-deploy.sync.reason")

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

	EventID             = attribute.Key("swarm-deploy.events.event.id")
	EventName           = attribute.Key("swarm-deploy.events.event.name")
	EventSubscriberName = attribute.Key("swarm-deploy.events.subscriber.name")
	EventQueueName      = attribute.Key("swarm-deploy.events.queue.name")

	ServiceVersion   = attribute.Key("service.version")
	ServiceBuildTime = attribute.Key("service.build.time")

	GenAIToolName          = attribute.Key("gen_ai.tool.name")
	GenAIToolType          = attribute.Key("gen_ai.tool.type")
	GenAIToolDescription   = attribute.Key("gen_ai.tool.description")
	GenAIToolCallArguments = attribute.Key("gen_ai.tool.call.arguments")
	GenAIToolCallResult    = attribute.Key("gen_ai.tool.call.result")
	GenAIConversationID    = attribute.Key("gen_ai.conversation.id")

	AssistantRequestSystemPromptChars = attribute.Key("swarm-deploy.assistant.request.system_prompt_chars")
	AssistantRequestHistoryChars      = attribute.Key("swarm-deploy.assistant.request.history_chars")
	AssistantRequestContextChars      = attribute.Key("swarm-deploy.assistant.request.context_chars")
	AssistantRequestUserMessageChars  = attribute.Key("swarm-deploy.assistant.request.user_message_chars")
	AssistantRequestToolsChars        = attribute.Key("swarm-deploy.assistant.request.tools_chars")
	AssistantRequestMessageCount      = attribute.Key("swarm-deploy.assistant.request.message_count")
	AssistantRequestToolCount         = attribute.Key("swarm-deploy.assistant.request.tool_count")
	AssistantRoute                    = attribute.Key("swarm-deploy.assistant.route")
	AssistantTerminalTool             = attribute.Key("swarm-deploy.assistant.terminal_tool")

	WebhookAuthAuthenticatorName          = attribute.Key("swarm-deploy.webhook.auth.authenticator.name").String
	WebhookAuthRequestAllowed             = attribute.Key("swarm-deploy.webhook.auth.request.allowed").Bool
	WebhookAuthRequestCredentialsProvided = attribute.Key("swarm-deploy.webhook.auth.request.credentials_provided").Bool
)
