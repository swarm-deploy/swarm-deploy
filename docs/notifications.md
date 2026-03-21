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
```
