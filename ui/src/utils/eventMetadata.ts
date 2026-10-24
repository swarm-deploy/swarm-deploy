export interface EventTypeMetadata {
  type: string;
  description: string;
}

export const eventTypeMetadata: EventTypeMetadata[] = [
  { type: "DeploySuccess", description: "Deployment completed successfully" },
  { type: "SyncManualStarted", description: "Manual sync started" },
  { type: "WebhookReceived", description: "Webhook request accepted" },
  { type: "NodeJoined", description: "Node joined the cluster" },
  { type: "NodeConnected", description: "Node became ready" },
  { type: "NodeDisconnected", description: "Node disconnected from cluster" },
  { type: "ServiceMissed", description: "Expected service is missing" },
  { type: "ServiceReplicasIncreased", description: "Service replica count increased" },
  { type: "ServiceReplicasDecreased", description: "Service replica count decreased" },
  { type: "ServiceRestarted", description: "Service restart requested" },
  { type: "ServicePruned", description: "Managed orphan service was removed" },
  { type: "NetworkCreated", description: "Network was created" },
  { type: "UserAuthenticated", description: "User signed in" },
  { type: "AssistantPromptInjectionDetected", description: "Prompt injection was detected" },
];

export const eventTypeDescriptions = new Map(
  eventTypeMetadata.map((metadata) => [metadata.type, metadata.description]),
);
