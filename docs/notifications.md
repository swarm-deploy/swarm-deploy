# Notifications

## Telegram templates

In a Telegram channel you can set:

- `botTokenPath` to read bot token from a file.
- `chatThreadId` to send to a specific thread/topic.
- `message` with Go template syntax.

Available template fields:

- `.status`
- `.success` (bool, `true` when `.status == "success"`)
- `.stack_name`
- `.service`
- `.image.full_name`
- `.image.version`
- `.commit`
- `.error`
- `.timestamp` (RFC3339)

Example:

```yaml
notifications:
  telegram:
    - name: ops
      botTokenPath: /run/secrets/telegram_bot_token
      chatId: "-1001234567890"
      chatThreadId: 42
      message: |
        deploy {{.status}}
        stack_name: {{.stack_name}}
        service: {{.service}}
        image.full_name: {{.image.full_name}}
        image.version: {{.image.version}}
        {{- if .error }}
        error: {{.error}}
        {{- end }}
```
