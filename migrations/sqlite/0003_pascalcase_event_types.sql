-- +goose Up
-- Event type identifiers are now PascalCase everywhere. Keep retained history
-- and pending durable deliveries readable without runtime legacy aliases.
UPDATE outbox_events
SET event_type = CASE event_type
    WHEN 'serviceCatalogUpdated' THEN 'ServiceCatalogUpdated'
    WHEN 'deploySuccess' THEN 'DeploySuccess'
    WHEN 'alertOpened' THEN 'AlertOpened'
    WHEN 'alertResolved' THEN 'AlertResolved'
    WHEN 'deployFailed' THEN 'DeployFailed'
    WHEN 'configurationChanged' THEN 'ConfigurationChanged'
    WHEN 'deployPreparationFailed' THEN 'DeployPreparationFailed'
    WHEN 'deployInterrupted' THEN 'DeployInterrupted'
    WHEN 'sendNotificationFailed' THEN 'SendNotificationFailed'
    WHEN 'syncManualStarted' THEN 'SyncManualStarted'
    WHEN 'webhookReceived' THEN 'WebhookReceived'
    WHEN 'nodeJoined' THEN 'NodeJoined'
    WHEN 'nodeConnected' THEN 'NodeConnected'
    WHEN 'nodeDisconnected' THEN 'NodeDisconnected'
    WHEN 'serviceMissed' THEN 'ServiceMissed'
    WHEN 'serviceReplicasIncreased' THEN 'ServiceReplicasIncreased'
    WHEN 'serviceReplicasDecreased' THEN 'ServiceReplicasDecreased'
    WHEN 'serviceRestarted' THEN 'ServiceRestarted'
    WHEN 'servicePruned' THEN 'ServicePruned'
    WHEN 'networkCreated' THEN 'NetworkCreated'
    WHEN 'userAuthenticated' THEN 'UserAuthenticated'
    WHEN 'assistantPromptInjectionDetected' THEN 'AssistantPromptInjectionDetected'
    ELSE event_type
END;

UPDATE event_history
SET event_type = CASE event_type
    WHEN 'serviceCatalogUpdated' THEN 'ServiceCatalogUpdated'
    WHEN 'deploySuccess' THEN 'DeploySuccess'
    WHEN 'alertOpened' THEN 'AlertOpened'
    WHEN 'alertResolved' THEN 'AlertResolved'
    WHEN 'deployFailed' THEN 'DeployFailed'
    WHEN 'configurationChanged' THEN 'ConfigurationChanged'
    WHEN 'deployPreparationFailed' THEN 'DeployPreparationFailed'
    WHEN 'deployInterrupted' THEN 'DeployInterrupted'
    WHEN 'sendNotificationFailed' THEN 'SendNotificationFailed'
    WHEN 'syncManualStarted' THEN 'SyncManualStarted'
    WHEN 'webhookReceived' THEN 'WebhookReceived'
    WHEN 'nodeJoined' THEN 'NodeJoined'
    WHEN 'nodeConnected' THEN 'NodeConnected'
    WHEN 'nodeDisconnected' THEN 'NodeDisconnected'
    WHEN 'serviceMissed' THEN 'ServiceMissed'
    WHEN 'serviceReplicasIncreased' THEN 'ServiceReplicasIncreased'
    WHEN 'serviceReplicasDecreased' THEN 'ServiceReplicasDecreased'
    WHEN 'serviceRestarted' THEN 'ServiceRestarted'
    WHEN 'servicePruned' THEN 'ServicePruned'
    WHEN 'networkCreated' THEN 'NetworkCreated'
    WHEN 'userAuthenticated' THEN 'UserAuthenticated'
    WHEN 'assistantPromptInjectionDetected' THEN 'AssistantPromptInjectionDetected'
    ELSE event_type
END;

UPDATE outbox_events
SET payload = json_set(payload, '$.event_type', CASE json_extract(payload, '$.event_type')
    WHEN 'serviceCatalogUpdated' THEN 'ServiceCatalogUpdated'
    WHEN 'deploySuccess' THEN 'DeploySuccess'
    WHEN 'alertOpened' THEN 'AlertOpened'
    WHEN 'alertResolved' THEN 'AlertResolved'
    WHEN 'deployFailed' THEN 'DeployFailed'
    WHEN 'configurationChanged' THEN 'ConfigurationChanged'
    WHEN 'deployPreparationFailed' THEN 'DeployPreparationFailed'
    WHEN 'deployInterrupted' THEN 'DeployInterrupted'
    WHEN 'sendNotificationFailed' THEN 'SendNotificationFailed'
    WHEN 'syncManualStarted' THEN 'SyncManualStarted'
    WHEN 'webhookReceived' THEN 'WebhookReceived'
    WHEN 'nodeJoined' THEN 'NodeJoined'
    WHEN 'nodeConnected' THEN 'NodeConnected'
    WHEN 'nodeDisconnected' THEN 'NodeDisconnected'
    WHEN 'serviceMissed' THEN 'ServiceMissed'
    WHEN 'serviceReplicasIncreased' THEN 'ServiceReplicasIncreased'
    WHEN 'serviceReplicasDecreased' THEN 'ServiceReplicasDecreased'
    WHEN 'serviceRestarted' THEN 'ServiceRestarted'
    WHEN 'servicePruned' THEN 'ServicePruned'
    WHEN 'networkCreated' THEN 'NetworkCreated'
    WHEN 'userAuthenticated' THEN 'UserAuthenticated'
    WHEN 'assistantPromptInjectionDetected' THEN 'AssistantPromptInjectionDetected'
    ELSE json_extract(payload, '$.event_type')
END)
WHERE json_type(payload, '$.event_type') = 'text';

UPDATE event_history
SET details = json_set(details, '$.event_type', CASE json_extract(details, '$.event_type')
    WHEN 'serviceCatalogUpdated' THEN 'ServiceCatalogUpdated'
    WHEN 'deploySuccess' THEN 'DeploySuccess'
    WHEN 'alertOpened' THEN 'AlertOpened'
    WHEN 'alertResolved' THEN 'AlertResolved'
    WHEN 'deployFailed' THEN 'DeployFailed'
    WHEN 'configurationChanged' THEN 'ConfigurationChanged'
    WHEN 'deployPreparationFailed' THEN 'DeployPreparationFailed'
    WHEN 'deployInterrupted' THEN 'DeployInterrupted'
    WHEN 'sendNotificationFailed' THEN 'SendNotificationFailed'
    WHEN 'syncManualStarted' THEN 'SyncManualStarted'
    WHEN 'webhookReceived' THEN 'WebhookReceived'
    WHEN 'nodeJoined' THEN 'NodeJoined'
    WHEN 'nodeConnected' THEN 'NodeConnected'
    WHEN 'nodeDisconnected' THEN 'NodeDisconnected'
    WHEN 'serviceMissed' THEN 'ServiceMissed'
    WHEN 'serviceReplicasIncreased' THEN 'ServiceReplicasIncreased'
    WHEN 'serviceReplicasDecreased' THEN 'ServiceReplicasDecreased'
    WHEN 'serviceRestarted' THEN 'ServiceRestarted'
    WHEN 'servicePruned' THEN 'ServicePruned'
    WHEN 'networkCreated' THEN 'NetworkCreated'
    WHEN 'userAuthenticated' THEN 'UserAuthenticated'
    WHEN 'assistantPromptInjectionDetected' THEN 'AssistantPromptInjectionDetected'
    ELSE json_extract(details, '$.event_type')
END)
WHERE details IS NOT NULL AND json_type(details, '$.event_type') = 'text';

-- +goose Down
SELECT 1;
