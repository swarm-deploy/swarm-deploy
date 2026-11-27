# Notifications

Event notifications are opt-in under `notifications.events`. Alert notifications are
automatic when Telegram and its `default` chat are configured. Telegram bot credentials,
proxy and named chat destinations are configured once in `notifications.messengers.telegram`.
Each Telegram rule accepts `chats` as a single name or array of names; omitting it
selects `default`. An unknown, empty or repeated destination is a configuration error.
The `message` field uses Go `text/template`; omitting it selects the standard template.
The removed `notifications.on` key is rejected; no compatibility alias is provided.
Existing configurations with `notifications.events.SendNotificationFailed` load
with a startup warning. Remove this retired route from the configuration.

## Telegram buttons

Add an optional `buttons` list to a Telegram event route or to an alert route's
`opened` or `resolved` section. Each button has a `text` label and a `url`.
Both use Go `text/template` with the same fields as the corresponding message.
Telegram displays the buttons in configuration order, one per row.

The rendered URL must be an absolute `http://`, `https://`, or `tg://` URL.
If the template renders an empty URL, swarm-deploy skips that button. The
[example below](#example) uses this to hide the deployment link when a `SyncFailed`
alert has no `DeploymentID`. Button templates are validated at startup, and
rendered URLs are checked before sending.

With `resolved.action: edit`, omitting `resolved.buttons` renders the current
`opened.buttons` templates again using the resolved alert's data. The resulting
links can change if the templates or alert data change. Set `buttons: []` under
`resolved` to clear the keyboard on edit, or supply a list to replace it.
Replies and fallback sends use the buttons configured under `resolved`.

## Alert lifecycle

Notification configuration uses the exact PascalCase alert names `SyncFailed`,
`NodeDisconnected`, `GitRepositoryUnavailable`, `ServiceDrift`,
`MessengerUnavailable`, and `MessengerMisconfigured`.
The special `default` key remains the fallback rule. Internal alert kinds and
fingerprints keep their existing snake_case identifiers.
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
`DeploymentID`, `ResolvedAt` (`*time.Time`), `Resolution` and `ResolutionReason`.
For example, `{{ .ResourceID }}` is valid; `.alert.Title` and `.event.StackName` are not.
`DeploymentID` is populated for synchronization failures created after a deployment
Attempt starts. A notification can link directly to its UI modal with
`/deployments?deployment_id={{ .DeploymentID }}`. Preparation failures may leave it empty.
Custom opened and resolved templates are validated when configuration loads, with the
exact path such as `notifications.alerts["SyncFailed"].telegram[0].resolved.message`
in an error.

The internal `AlertOpened` and `AlertResolved` transitions join the SQLite transaction
that changes the alert, but are not written to Event History. Telegram calls run later
through the existing Outbox, independently for each physical recipient. Retries,
leases, replay and acknowledgement are shared with event notifications. Opened delivery
precedes resolved delivery for each recipient. A temporary Telegram error is retried;
an invalid original message reference falls back to a regular resolution message.
Without an original message ID, resolution also sends a regular message.

The initial SQLite schema has one `notification_messages`
row per alert ID, messenger, bot identity, chat ID and thread ID. It stores the initial
message ID and send time, the processed resolution time and action, and a reply message
ID when applicable. Resolved links are removed only after their lifecycle events have
left the Outbox; open links have no TTL. There is no second delivery queue. Successful
database recording prevents repeat sends on replay, but a crash after Telegram accepts
a call and before its receipt is stored can duplicate a message. Telegram delivery is
therefore at least once, not exactly once.

## Messenger health

Telegram request attempts for event notifications and ordinary alerts publish
internal messenger health observations. Transport
failures and HTTP 5xx responses open or update one `messenger_unavailable` alert with
resource ID `telegram`. Any non-5xx Telegram response resolves it, even when the
notification itself is rejected. Configuration rejections open or update a
`messenger_misconfigured` alert with resource ID `telegram:<chat-name>`; a successful
delivery to that same named chat resolves it. Chat IDs, thread IDs and bot credentials
are never part of these resource IDs.

These observations are durable Event Bus inputs but are neither public notification
event names nor Event History entries. Availability alerts are not routed through
Telegram. Configuration alerts exclude their own failing named chat, while other
configured alert destinations may still receive the lifecycle notification. Deliveries
of messenger health alerts suppress observations to prevent recursion. A delivery to
a persisted recipient whose chat is no longer configured only reports availability.

## Template API

Payloads are typed contracts rather than internal event, alert, deployment, or Compose
models. Public fields use Go names and are available at the template root:

```gotemplate
{{ .Status }}
{{ .StackName }}
{{ range .Services }}{{ .Name }}: {{ .Image }}{{ end }}
```

All payloads have `ID`, `Type`, and `OccurredAt` (`time.Time`, UTC). Most also provide
`Message`. Source-specific fields are:

| Event types | Payload fields |
| --- | --- |
| `DeploySuccess` | `ID`, `DeploymentID`, `AttemptID`, `Type`, `Status`, `StackName`, `Services` (`Name`, `Image`), `Commit`, `OccurredAt` |
| `NodeJoined`, `NodeConnected`, `NodeDisconnected` | `ID`, `Type`, `NodeID`, `NodeName`, `Role`, `Status`, `Message`, `OccurredAt` |
| `ServiceMissed`, `ServiceDrifted`, `ServiceDriftFixed`, `ServicePruned`, `ServiceReplicasIncreased`, `ServiceReplicasDecreased`, `ServiceRestarted` | `ID`, `Type`, `StackName`, `ServiceName`, `Commit`, `Reason`, `Username`, `PreviousReplicas`, `CurrentReplicas`, `Message`, `OccurredAt` |
| `ServiceCatalogUpdated`, `SyncManualStarted`, `WebhookReceived`, `NetworkCreated` | `ID`, `Type`, `StackName`, `Username`, `Queued`, `NetworkName`, `NetworkID`, `Driver`, `Message`, `OccurredAt` |
| `UserAuthenticated` | `ID`, `Type`, `Username`, `Message`, `OccurredAt` |
| `AssistantPromptInjectionDetected` | `ID`, `Type`, `Detector`, `Username`, `Message`, `OccurredAt` |

Shared template fields and serialized JSON names match Event History semantics:

| Template field | JSON and Event History attribute |
| --- | --- |
| `.StackName` | `stack_name` |
| `.ServiceName` | `service_name` |
| `.DeploymentID` | `deployment_id` |
| `.AttemptID` | `attempt_id` |
| `.Commit` | `commit` |
| `.Reason` | `reason` |
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

`DeploySuccess.Services` lists only the services selected for that deployment Attempt.
Unchanged services are omitted from its notification payload. An Attempt without
selected services, such as one that only prunes removed services, sends no
`DeploySuccess` notification; `ServicePruned` reports each removal.

`DeployFailed`, `DeployPreparationFailed`, and `DeployInterrupted` are internal lifecycle
events. They cannot be configured under `notifications.events`; synchronization failures
open or update the `sync_failed` alert and use Alert Notifications instead.
A reconciliation step that fails after a successful Attempt also reports its diagnostic
through this alert.

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

Every custom template is parsed and executed against a representative payload while the
configuration is loaded.
Unknown fields, invalid functions, syntax errors, and execution errors reject the entire
configuration before modules are initialized. The error includes the event type and YAML
path, for example:

```text
notifications.events["DeploySuccess"].telegram[0].message: invalid template: execute template: template: notification:1:3: executing "notification" at <.Stack>: can't evaluate field Stack in type eventnotify.DeploymentPayload
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
          buttons:
            - text: "Open {{ .StackName }} deployment"
              url: "https://swarm-deploy.example/deployments?deployment_id={{ .DeploymentID | urlquery }}"
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
            buttons:
              - text: "Open alert"
                url: "https://swarm-deploy.example/events?tab=alerts&alert_id={{ .ID | urlquery }}"
    SyncFailed:
      telegram:
        - chats: deployments
          opened:
            message: "🔴 Synchronization failed: {{ .ResourceID }}"
            buttons:
              - text: "Open alert"
                url: "https://swarm-deploy.example/events?tab=alerts&alert_id={{ .ID | urlquery }}"
              - text: "Open failed deployment"
                url: '{{ with .DeploymentID }}https://swarm-deploy.example/deployments?deployment_id={{ . | urlquery }}{{ end }}'
          resolved:
            action: edit
            message: "✅ Synchronization recovered: {{ .ResourceID }}"
```

## Web Push with ntfy

Set `web.pwa.ntfyTopic` to an ntfy topic. Users can then enable notifications
on the Events & Alerts page. The default (empty) disables the integration. Each
browser opts in independently. Use HTTPS. On iOS, open the installed home-screen
app. The PWA refreshes an existing subscription on startup.

Route exactly `<web.basePath>/ntfy/v1/config` (GET) and
`<web.basePath>/ntfy/v1/webpush` (POST/DELETE) through the app's authenticated
reverse proxy to ntfy's corresponding `/v1/...` endpoints. Restrict topic read
access to the intended users and keep publishing on the server side. ntfy owns
the VAPID keys, subscription storage and expiry. This integration uses the web
client API of ntfy 2.38.2. Keep ntfy’s web root enabled internally so its
subscription API accepts requests. A device subscription survives signing out
of the app. Use **Disable notifications** to revoke it on a shared device.

Publish events using the existing `notifications.events.<event>.custom` route.
A push displays ntfy's title and message; tapping it opens the native `click`
URL within the app, or Events & Alerts by default. To build a link from webhook
fields, an ntfy template can render the message as JSON with string fields
`body` and `url`. Relative links resolve against the app's base path.
