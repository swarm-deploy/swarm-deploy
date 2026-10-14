# Notifications

Event notifications are opt-in under `notifications.events`. Alert notifications are
automatic when Telegram and its `default` chat are configured. Telegram bot credentials,
proxy and named chat destinations are configured once in `notifications.messengers.telegram`.
Each Telegram rule accepts `chats` as a single name or array of names; omitting it
selects `default`. An unknown, empty or repeated destination is a configuration error.
The `message` field uses Go `text/template`; omitting it selects the standard template.
The removed `notifications.on` key is rejected; no compatibility alias is provided.

## Alert lifecycle

Alert Management currently creates `deploy_failed` and `node_disconnected` alerts.
Use those exact keys under `notifications.alerts`; `default` is the fallback rule.
A kind rule replaces `alerts.default`, and an omitted `alerts.default` selects built-in
opened and resolved messages in Telegram chat `default`. Configuring Telegram requires
that chat. Without Telegram, alerts continue to be stored and shown in the UI.

An alert sends one new message when opened. Repeated detections update its alert record
without sending another message. Resolution replies to or edits that initial message;
`reply` is the default action. A new opening after resolution has a new alert ID and a
new initial message.

Each alert rule may route to one chat (`chats: deployments`) or several
(`chats: [default, infrastructure]`). An omitted `chats` selects `default`.
Empty lists, unknown names and repeated names are errors. The initial physical chat ID
and topic are persisted, so resolution uses the original destination even if an alias
changes or is removed at the next restart. If the original bot credentials are no
longer available, its Outbox delivery records an error for inspection and replay.

Alert templates use the shared Go template renderer. The typed fields at the root are
`ID`, `Kind`, `Title`, `Message`, `ResourceType`, `ResourceID`, `OpenedAt`,
`ResolvedAt` (`*time.Time`), `Resolution` and `ResolutionReason`.
For example, `{{ .ResourceID }}` is valid; `.alert.Title` and `.event.StackName` are not.
Custom opened and resolved templates are validated when configuration loads, with the
exact path such as `notifications.alerts["deploy_failed"].telegram[0].resolved.message`
in an error.

The internal `AlertOpened` and `AlertResolved` transitions join the SQLite transaction
that changes the alert, but are not written to Event History. Telegram calls run later
through the existing Outbox, independently for each physical recipient. Retries,
leases, replay and acknowledgement are shared with event notifications. Opened delivery
precedes resolved delivery for each recipient. A temporary Telegram error is retried;
an invalid original message reference falls back to a regular resolution message.
Without an original message ID, resolution also sends a regular message.

SQLite migration `0002_notification_messages.sql` adds one `notification_messages`
row per alert ID, messenger, bot identity, chat ID and thread ID. It stores the initial
message ID and send time, the processed resolution time and action, and a reply message
ID when applicable. Resolved links are removed only after their lifecycle events have
left the Outbox; open links have no TTL. There is no second delivery queue. Successful
database recording prevents repeat sends on replay, but a crash after Telegram accepts
a call and before its receipt is stored can duplicate a message. Telegram delivery is
therefore at least once, not exactly once.

## Template API

Payloads are typed contracts rather than internal event, alert, deployment, or Compose
models. Public fields use Go names and are available at the template root:

```gotemplate
{{ .Status }}
{{ .StackName }}
{{ if .Error }}error: {{ .Error }}{{ else }}no error{{ end }}
{{ range .Services }}{{ .Name }}: {{ .Image }}{{ end }}
```

All payloads have `ID`, `Type`, and `OccurredAt` (`time.Time`, UTC). Most also provide
`Message`. Source-specific fields are:

| Event types | Payload fields |
| --- | --- |
| `DeploySuccess`, `SyncFailed` | `ID`, `DeploymentID`, `Type`, `Status`, `StackName`, `Services` (`Name`, `Image`), `Commit`, `Error`, `ErrorCode`, `Stage`, `OccurredAt` |
| `NodeJoined`, `NodeConnected`, `NodeDisconnected` | `ID`, `Type`, `NodeID`, `NodeName`, `Role`, `Status`, `Message`, `OccurredAt` |
| `ServiceMissed`, `ServicePruned`, `ServiceReplicasIncreased`, `ServiceReplicasDecreased`, `ServiceRestarted` | `ID`, `Type`, `StackName`, `ServiceName`, `Commit`, `Username`, `PreviousReplicas`, `CurrentReplicas`, `Message`, `OccurredAt` |
| `ServiceCatalogUpdated`, `SyncManualStarted`, `WebhookReceived`, `NetworkCreated` | `ID`, `Type`, `StackName`, `Username`, `Queued`, `NetworkName`, `NetworkID`, `Driver`, `Message`, `OccurredAt` |
| `UserAuthenticated` | `ID`, `Type`, `Username`, `Message`, `OccurredAt` |
| `AssistantPromptInjectionDetected` | `ID`, `Type`, `Detector`, `Username`, `Message`, `OccurredAt` |
| `SendNotificationFailed` | `ID`, `Type`, `EventType`, `Destination`, `Channel`, `Error`, `Message`, `OccurredAt` |

Shared template fields and serialized JSON names match Event History semantics:

| Template field | JSON and Event History attribute |
| --- | --- |
| `.StackName` | `stack_name` |
| `.ServiceName` | `service_name` |
| `.DeploymentID` | `deployment_id` |
| `.Commit` | `commit` |
| `.Username` | `username` |
| `.NodeID`, `.NodeName` | `node_id`, `node_name` |
| `.NetworkID`, `.NetworkName` | `network_id`, `network_name` |
| `.Status` | `status` |
| `.Error` | `error` (bounded, secret-masked diagnostic) |
| `.ErrorCode` | `error_code` (stable failure category) |
| `.Stage` | `stage` |

The former `.TriggeredBy` template field and `triggered_by` JSON field have been
removed; manual synchronization templates must use `.Username`. The obsolete
`stack` and `service` aliases are not available in notification JSON.

`SyncFailed` covers the complete synchronization-failure lifecycle. It receives ordinary
failures (`Status == "failed"`), preparation failures
(`"preparation_failed"`), and interrupted attempts (`"interrupted"`). Fields that do not
apply to a state have their Go zero value: strings are empty, numeric values are zero,
slices may be empty. Test optional fields with `if` before rendering them.

## Date formatting

The shared `date` function accepts a standard Go time layout and either `time.Time` or
`*time.Time`:

```gotemplate
{{ date "2006-01-02 15:04" .OccurredAt }}
```

Dates are always converted to UTC before formatting, so output does not depend on the
server timezone. If a future or source-specific contract exposes a nullable time, a nil
`*time.Time` renders as an empty string.

## Validation and runtime errors

Every custom template is parsed and executed against representative payloads while the
configuration is loaded. `SyncFailed` is checked against all three failure states.
Unknown fields, invalid functions, syntax errors, and execution errors reject the entire
configuration before modules are initialized. The error includes the event type and YAML
path, for example:

```text
notifications.events["SyncFailed"].telegram[0].message: invalid template: execute template: template: notification:1:3: executing "notification" at <.Stack>: can't evaluate field Stack in type notifications.Payload
```

The current application loads configuration only at startup; there is no hot-reload path.
If hot reload is added, callers must keep the active configuration until `config.Load`
returns a fully validated replacement.

Representative payload validation cannot prove every runtime branch safe. A runtime
rendering error is returned as a normal delivery error: it does not panic and the Event
Bus records/retries that destination independently according to the existing delivery
semantics.

## Example

```yaml
notifications:
  messengers:
    telegram:
      botTokenPath: /run/secrets/telegram_bot_token
      retries: 3
      chats:
        default:
          id: "-1001234567890"
          threadId: 42
        deployments:
          id: "-1001234567890"
          threadId: 155
  events:
    DeploySuccess:
      telegram:
        - chats: deployments
          message: |
            💚 deploy {{ .Status }}
            stack: {{ .StackName }}
            at: {{ date "2006-01-02 15:04" .OccurredAt }} UTC
    NodeJoined:
      telegram:
        - chats: [default, deployments]
          message: "node joined: {{ .NodeName }}"
  alerts:
    default:
      telegram:
        - opened:
            message: "🚨 {{ .Title }}: {{ .Message }}"
          resolved:
            action: reply
            message: "✅ {{ .Title }}: {{ .Resolution }}"
    deploy_failed:
      telegram:
        - chats: deployments
          opened:
            message: "🔴 Deployment failed: {{ .ResourceID }}"
          resolved:
            action: edit
            message: "✅ Deployment recovered: {{ .ResourceID }}"
```
