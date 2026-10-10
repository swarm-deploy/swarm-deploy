# Notifications

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

`deployFailed` is the compatibility subscription for the complete deployment-failure
lifecycle. It also receives `deployPreparationFailed` when no Deployment exists yet and
`deployInterrupted` when an apply outcome is unknown. These signals contain only stable
safe categories; raw loader, environment, script and Docker errors are never persisted.
