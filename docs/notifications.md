# Notifications

Notification channels are configured under `notifications.on` by event type. Telegram
channels accept a `message` written with the standard Go `text/template` syntax. If
`message` is omitted, Swarm Deploy uses the standard template owned by the module that
produces the notification.

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
| `deploySuccess`, `deployFailed` | `ID`, `DeploymentID`, `Type`, `Status`, `StackName`, `Services` (`Name`, `Image`), `Commit`, `Error`, `ErrorCode`, `Stage`, `OccurredAt` |
| `nodeJoined`, `nodeConnected`, `nodeDisconnected` | `ID`, `Type`, `NodeID`, `NodeName`, `Role`, `Status`, `Message`, `OccurredAt` |
| `serviceMissed`, `servicePruned`, `serviceReplicasIncreased`, `serviceReplicasDecreased`, `serviceRestarted` | `ID`, `Type`, `StackName`, `ServiceName`, `Commit`, `Username`, `PreviousReplicas`, `CurrentReplicas`, `Message`, `OccurredAt` |
| `serviceCatalogUpdated`, `syncManualStarted`, `webhookReceived`, `networkCreated` | `ID`, `Type`, `StackName`, `TriggeredBy`, `Queued`, `NetworkName`, `NetworkID`, `Driver`, `Message`, `OccurredAt` |
| `userAuthenticated` | `ID`, `Type`, `Username`, `Message`, `OccurredAt` |
| `assistantPromptInjectionDetected` | `ID`, `Type`, `Detector`, `Username`, `Message`, `OccurredAt` |
| `sendNotificationFailed` | `ID`, `Type`, `EventType`, `Destination`, `Channel`, `Error`, `Message`, `OccurredAt` |

`deployFailed` covers the complete deployment-failure lifecycle. It receives ordinary
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
configuration is loaded. `deployFailed` is checked against all three failure states.
Unknown fields, invalid functions, syntax errors, and execution errors reject the entire
configuration before modules are initialized. The error includes the event type and YAML
path, for example:

```text
notifications.on["deployFailed"].telegram[0].message: invalid template: execute template: template: notification:1:3: executing "notification" at <.Stack>: can't evaluate field Stack in type notifications.Payload
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
      retries: 3
  on:
    deploySuccess:
      telegram:
        - name: ops-success
          botTokenPath: /run/secrets/telegram_bot_token
          chatId: "-1001234567890"
          chatThreadId: 42
          message: |
            💚 deploy {{ .Status }}
            stack: {{ .StackName }}
            at: {{ date "2006-01-02 15:04" .OccurredAt }} UTC
            {{ range .Services -}}
            service: {{ .Name }}
            image: {{ .Image }}
            {{ end }}

    deployFailed:
      telegram:
        - name: ops-failed
          botTokenPath: /run/secrets/telegram_bot_token
          chatId: "-1001234567890"
          message: |
            🔻 deploy {{ .Status }}
            stack: {{ .StackName }}
            {{ if .Error }}error: {{ .Error }}{{ end }}

    userAuthenticated:
      telegram:
        - name: ops-auth
          botTokenPath: /run/secrets/telegram_bot_token
          chatId: "-1001234567890"
          message: 'user authenticated: {{ .Username }}'
```
