# Notifications

Notifications have two independent sources:

- `notifications.on` — a notification per event (a fact that happened), configured per event type.
- `notifications.alerts` — notifications about the lifecycle of an alert (problem opened → resolved).

## Telegram templates

In a Telegram channel you can set:

- `botTokenPath` to read bot token from a file.
- `chatThreadId` to send to a specific thread/topic.
- `message` with Go template syntax.

Available template fields:

- `.event.stack_name`
- `.event.services.*.name`
- `.event.services.*.image`
- `.event.commit`
- `.event.error`
- `.event.username` (for `userAuthenticated`)
- `.event.node_id` (for `nodeJoined` / `nodeConnected` / `nodeDisconnected`)
- `.event.node_name` (for `nodeJoined` / `nodeConnected` / `nodeDisconnected`)
- `.event.role` (for `nodeJoined`, when available)
- `.event.status` (for `nodeConnected` / `nodeDisconnected`)

Example:

```yaml
# Notification settings.
notifications:
  on:
    deploySuccess:
      telegram:
        - name: ops-success
          # Path to file with bot token.
          botTokenPath: /run/secrets/telegram_bot_token
          chatId: "-1001234567890"
          # Chat/channel ID.
          chatThreadId: 42
          # Message text template.
          message: |
            💚 deploy successful
            stack_name: {{.event.stack_name}}
            {{ range .event.services }}
            service: {{.name}}
            image: {{.image}}
            ---
            {{ end }}

    deployFailed:
      telegram:
        - name: ops-failed
          # Path to file with bot token.
          botTokenPath: /run/secrets/telegram_bot_token
          # Chat/channel ID.
          chatId: "-1001234567890"
          # Message text template.
          message: |
            🔻deploy failed
            stack_name: {{.event.stack_name}}
            {{ range .event.services }}
            service: {{.name}}
            image: {{.image}}
            ---
            {{ end }}
            error: {{.error}}

    userAuthenticated:
      telegram:
        - name: ops-auth
          # Path to file with bot token.
          botTokenPath: /run/secrets/telegram_bot_token
          # Chat/channel ID.
          chatId: "-1001234567890"
          # Message text template.
          message: |
            user authenticated
            username: {{.event.username}}
```

## Alerts

An alert is opened when a deployment fails and resolved when the next deployment of the stack succeeds.
Repeated failures update the open alert and do not produce notifications.

```yaml
notifications:
  alerts:
    # send (default): a message when the alert is opened and a separate message when it is resolved.
    # edit: a message when the alert is opened; it is edited when the alert is resolved.
    # reply: a message when the alert is opened; the resolution is sent as a reply to it.
    mode: reply
    # How long the "opened" message can be edited (default 24h).
    correlationTtl: 24h
    telegram:
      - name: ops-alerts
        botTokenPath: /run/secrets/telegram_bot_token
        chatId: "-1001234567890"
        # Optional template with .alert, .resolution and .status ("open" | "resolved").
        message: |
          {{.alert.Title}}: {{.alert.ResourceID}} ({{.status}})
          {{.alert.Message}}
```

Notes:

- Only Telegram supports alert notifications (it supports editing messages).
- Deployment errors that report `Temporary() == true` (including wrapped ones) open an alert only after
  3 consecutive failure events; permanent errors open it immediately. A successful deployment resets the wait.
  The wait counter is kept in memory.
- Alert state is stored independently of notifications: Telegram failures are logged and never affect alerts or GitOps.
- In `edit` and `reply` modes, `message_id` of the sent message is stored in `notification-deliveries.state.json`
  in `dataDir` for `correlationTtl`, so it survives restarts. If the record is missing or expired,
  or Telegram reports the original message as unavailable (deleted), the resolution is sent as a separate message.
  If an edit fails for a transient reason the error is logged and the record is kept; the notification is not retried automatically.
