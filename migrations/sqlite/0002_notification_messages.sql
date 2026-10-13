-- +goose Up
-- One physical recipient per alert lifecycle. No alert FK: retention may remove a
-- resolved alert while its durable resolution delivery is still pending.
CREATE TABLE notification_messages (
    alert_id TEXT NOT NULL,
    messenger TEXT NOT NULL CHECK (messenger = 'telegram'),
    bot_identity TEXT NOT NULL,
    chat_id TEXT NOT NULL,
    thread_id INTEGER NOT NULL DEFAULT 0,
    opened_message_id INTEGER,
    opened_at TIMESTAMP CHECK (opened_at IS NULL OR typeof(opened_at) = 'integer'),
    resolved_at TIMESTAMP CHECK (resolved_at IS NULL OR typeof(resolved_at) = 'integer'),
    resolved_action TEXT CHECK (resolved_action IS NULL OR resolved_action IN ('send','reply','edit')),
    reply_message_id INTEGER,
    PRIMARY KEY (alert_id, messenger, bot_identity, chat_id, thread_id)
);
CREATE INDEX idx_notification_messages_bot ON notification_messages(bot_identity, chat_id, thread_id);

-- +goose Down
DROP TABLE notification_messages;
