# Notifications

## Telegram templates

In a Telegram channel you can set:

- `botTokenPath` to read bot token from a file.
- `chatThreadId` to send to a specific thread/topic.
- `message` with Go template syntax.

Available template fields:

- For `deploySuccess` and `deployFailed`:
  - `.event.StackName`
  - `.event.Services`
  - `.event.Commit`
  - `.event.Error`
- For `syncManualStarted`:
  - `.event` (event object without fields)

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
            stack_name: {{.event.StackName}}
            {{ range .event.Services }}
            service: {{.Name}}
            image: {{.Image}}
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
            stack_name: {{.event.StackName}}
            {{ range .event.Services }}
            service: {{.Name}}
            image: {{.Image}}
            ---
            {{ end }}
            error: {{.event.Error}}

    syncManualStarted:
      telegram:
        - name: ops-manual-sync
          botTokenPath: /run/secrets/telegram_bot_token
          chatId: "-1001234567890"
          message: |
            sync started manually
```
