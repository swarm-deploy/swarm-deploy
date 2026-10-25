package alerts

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"html"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/swarm-deploy/swarm-deploy/internal/config"
	alertmodel "github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement/model"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/dispatcher"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/outbox"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/notifications/notifiers"
	"github.com/swarm-deploy/swarm-deploy/internal/storage"
)

const (
	telegramMessageLimit     = 4096
	telegramHTMLParseMode    = "HTML"
	syncFailedErrorTruncated = "\n\n[error truncated]"
)

// SubscriptionID identifies one physical destination across configuration alias changes.
func SubscriptionID(recipient events.AlertRecipient) string {
	return "alert:telegram:" + recipient.BotIdentity + ":" +
		base64.RawURLEncoding.EncodeToString([]byte(recipient.ChatID)) + ":" + strconv.FormatInt(recipient.ThreadID, 10)
}

func parseSubscriptionID(id string) (events.AlertRecipient, error) {
	parts := strings.Split(id, ":")
	if len(parts) != 5 || parts[0] != "alert" || parts[1] != "telegram" {
		return events.AlertRecipient{}, fmt.Errorf("invalid alert subscription ID")
	}
	chat, err := base64.RawURLEncoding.DecodeString(parts[3])
	if err != nil {
		return events.AlertRecipient{}, err
	}
	thread, err := strconv.ParseInt(parts[4], 10, 64)
	if err != nil {
		return events.AlertRecipient{}, err
	}
	return events.AlertRecipient{BotIdentity: parts[2], ChatID: string(chat), ThreadID: thread}, nil
}

// Register subscribes all configured and persisted physical destinations to both transitions.
func Register(
	ctx context.Context, bus dispatcher.Dispatcher, db *storage.Database, spec config.NotificationSpec,
) error {
	store := NewStore(db)
	if err := store.PruneCompleted(ctx); err != nil {
		return err
	}
	recipients := make(map[events.AlertRecipient]bool)
	for _, recipient := range spec.AlertRecipients("default") {
		recipients[recipient] = true
	}
	for kind := range spec.Alerts {
		if kind == "default" {
			continue
		}
		for _, recipient := range spec.AlertRecipients(kind) {
			recipients[recipient] = true
		}
	}
	known, err := store.KnownRecipients(ctx)
	if err != nil {
		return err
	}
	for _, recipient := range known {
		recipients[recipient] = true
	}
	pending, err := store.PendingSubscriptions(ctx)
	if err != nil {
		return err
	}
	for _, id := range pending {
		recipient, parseErr := parseSubscriptionID(id)
		if parseErr != nil {
			return fmt.Errorf("recover alert notification subscription: %w", parseErr)
		}
		recipients[recipient] = true
	}
	for recipient := range recipients {
		id := SubscriptionID(recipient)
		destinationKey := outbox.NotificationDestinationKey(
			"telegram", recipient.BotIdentity, recipient.ChatID, strconv.FormatInt(recipient.ThreadID, 10),
		)
		channel := notificationChannelName(spec, recipient, destinationKey)
		handler := outbox.ExternalNotification(
			&Subscriber{store: store, spec: spec, recipient: recipient, now: time.Now},
			"telegram", channel, destinationKey,
		)
		if err = bus.Subscribe(events.TypeNameAlertOpened, id, handler); err != nil {
			return err
		}
		if err = bus.Subscribe(events.TypeNameAlertResolved, id, handler); err != nil {
			return err
		}
	}
	return nil
}

func notificationChannelName(
	spec config.NotificationSpec, recipient events.AlertRecipient, destinationKey string,
) string {
	names := make([]string, 0, len(spec.Messengers.Telegram.Chats))
	for name := range spec.Messengers.Telegram.Chats {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		chat := spec.Messengers.Telegram.Chats[name]
		if spec.TelegramBotIdentity() == recipient.BotIdentity &&
			chat.ID == recipient.ChatID && chat.ThreadID == recipient.ThreadID {
			return name
		}
	}
	return "recipient-" + destinationKey[len("telegram:"):len("telegram:")+12]
}

// Subscriber delivers both lifecycle phases for one physical Telegram recipient.
type Subscriber struct {
	store      *Store
	spec       config.NotificationSpec
	recipient  events.AlertRecipient
	now        func() time.Time
	apiBaseURL string
}

// Handle applies an alert transition, with durable per-recipient progress.
func (s *Subscriber) Handle(ctx context.Context, envelope events.Envelope) error {
	_, err := s.HandleNotification(ctx, envelope)
	return err
}

// HandleNotification reports whether an external notification was sent.
func (s *Subscriber) HandleNotification(ctx context.Context, envelope events.Envelope) (bool, error) {
	if err := s.store.PruneCompleted(ctx); err != nil {
		return false, err
	}
	switch event := envelope.Event.(type) {
	case *events.AlertOpened:
		for _, recipient := range event.Recipients {
			if recipient == s.recipient {
				return s.opened(ctx, event.Alert)
			}
		}
		return false, nil
	case *events.AlertResolved:
		return s.resolved(ctx, event)
	default:
		return false, nil
	}
}

func (s *Subscriber) opened(ctx context.Context, alert events.AlertSnapshot) (bool, error) {
	if err := s.store.Ensure(ctx, alert.ID, s.recipient); err != nil {
		return false, err
	}
	link, err := s.store.Get(ctx, alert.ID, s.recipient)
	if err != nil {
		return false, err
	}
	// Do not replay an opening after the alert was already resolved.
	if link.ResolvedAt.Valid {
		return false, nil
	}
	if link.OpenedAt.Valid {
		return true, nil
	}
	rule := s.rule(alert.Kind)
	payload := alert
	parseMode := ""
	if alert.Kind == string(alertmodel.AlertKindGitRepositoryUnavailable) && strings.TrimSpace(rule.Opened.Message) == "" {
		payload.ResourceID = html.EscapeString(alert.ResourceID)
		payload.Title = html.EscapeString(alert.Title)
		payload.Message = html.EscapeString(alert.Message)
		parseMode = telegramHTMLParseMode
	} else if alert.Kind == string(alertmodel.AlertKindSyncFailed) && strings.TrimSpace(rule.Opened.Message) == "" {
		payload = defaultSyncFailedPayload(alert)
		parseMode = telegramHTMLParseMode
	}
	notifier, err := s.notifier(rule.AlertOpenedTemplate(alert.Kind), parseMode)
	if err != nil {
		return false, err
	}
	receipt, err := notifier.Notify(ctx, notifiers.Message{Payload: payload, Action: notifiers.ActionSend})
	if err != nil {
		return false, err
	}
	if err = s.store.MarkOpened(ctx, alert.ID, s.recipient, receipt, s.now()); err != nil {
		return false, err
	}
	return true, nil
}

func (s *Subscriber) resolved(
	ctx context.Context, event *events.AlertResolved,
) (bool, error) {
	alert := event.Alert
	link, deliver, err := s.resolutionLink(ctx, event)
	if err != nil {
		return false, err
	}
	if !deliver {
		return false, nil
	}
	if link.ResolvedAt.Valid {
		return true, nil
	}
	rule := s.rule(alert.Kind)
	payload := alert
	parseMode := ""
	if alert.Kind == string(alertmodel.AlertKindGitRepositoryUnavailable) &&
		strings.TrimSpace(rule.Resolved.Message) == "" {
		payload.ResourceID = html.EscapeString(alert.ResourceID)
		parseMode = telegramHTMLParseMode
	}
	notifier, err := s.notifier(rule.AlertResolvedTemplate(alert.Kind), parseMode)
	if err != nil {
		return false, err
	}
	action := notifiers.ActionSend
	if link.OpenedMessageID.Valid {
		action = notifiers.Action(rule.AlertResolvedAction())
	}
	receipt, err := notifier.Notify(ctx, notifiers.Message{
		Payload: payload, Action: action, TargetMessageID: link.OpenedMessageID.Int64,
	})
	if err != nil {
		return false, err
	}
	if err = s.store.MarkResolved(ctx, alert.ID, s.recipient, receipt, s.now()); err != nil {
		return false, err
	}
	return true, nil
}

func (s *Subscriber) resolutionLink(
	ctx context.Context, event *events.AlertResolved,
) (MessageLink, bool, error) {
	link, err := s.store.Get(ctx, event.Alert.ID, s.recipient)
	if err == nil {
		return link, true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return MessageLink{}, false, err
	}
	deliver, err := s.shouldResolveWithoutLink(ctx, event)
	if err != nil || !deliver {
		return MessageLink{}, deliver, err
	}
	if err = s.store.Ensure(ctx, event.Alert.ID, s.recipient); err != nil {
		return MessageLink{}, false, err
	}
	link, err = s.store.Get(ctx, event.Alert.ID, s.recipient)
	return link, err == nil, err
}

func (s *Subscriber) shouldResolveWithoutLink(ctx context.Context, event *events.AlertResolved) (bool, error) {
	openingRecipients, openingPending, err := s.store.OpeningRecipients(ctx, event.Alert.ID)
	if err != nil {
		return false, err
	}
	if openingPending {
		return containsRecipient(openingRecipients, s.recipient), nil
	}
	hasLinks, err := s.store.HasAny(ctx, event.Alert.ID)
	if err != nil {
		return false, err
	}
	return !hasLinks && containsRecipient(event.Recipients, s.recipient), nil
}

func containsRecipient(recipients []events.AlertRecipient, target events.AlertRecipient) bool {
	for _, recipient := range recipients {
		if recipient == target {
			return true
		}
	}
	return false
}

func (s *Subscriber) rule(kind string) config.TelegramAlertChannel {
	rules := s.spec.AlertTelegramRoutes(kind)
	for _, rule := range rules {
		for _, name := range (config.TelegramChannel{Chats: rule.Chats}).ChatNames() {
			chat := s.spec.Messengers.Telegram.Chats[name]
			if chat.ID == s.recipient.ChatID && chat.ThreadID == s.recipient.ThreadID {
				return rule
			}
		}
	}
	if len(rules) > 0 {
		return rules[0]
	}
	return config.TelegramAlertChannel{}
}

func (s *Subscriber) notifier(message, parseMode string) (notifiers.Notifier, error) {
	telegram := s.spec.Messengers.Telegram
	if s.spec.TelegramBotIdentity() != s.recipient.BotIdentity {
		return nil, fmt.Errorf("telegram credentials unavailable for alert recipient")
	}
	return notifiers.NewTelegramNotifier(s.recipient.ChatID, string(telegram.BotToken.Content), s.recipient.ChatID,
		notifiers.TelegramOptions{ChatThreadID: s.recipient.ThreadID, Message: message,
			ParseMode: parseMode, Retries: telegram.Retries,
			SOCKS5Address: telegram.Proxy.SOCKS5.Address.Value, APIBaseURL: s.apiBaseURL})
}

func defaultSyncFailedPayload(alert events.AlertSnapshot) events.AlertSnapshot {
	header := "🚨 " + alert.Title + "\n\nStack: " + alert.ResourceID + "\n\nError:\n"
	available := telegramMessageLimit - telegramTextLength(header)
	alert.Title = html.EscapeString(alert.Title)
	alert.ResourceID = html.EscapeString(alert.ResourceID)
	alert.Message = html.EscapeString(truncateTelegramText(alert.Message, available, syncFailedErrorTruncated))
	return alert
}

func truncateTelegramText(value string, limit int, suffix string) string {
	if limit <= 0 {
		return ""
	}
	if telegramTextLength(value) <= limit {
		return value
	}
	suffixLength := telegramTextLength(suffix)
	if limit <= suffixLength {
		return takeTelegramText(suffix, limit)
	}
	return takeTelegramText(value, limit-suffixLength) + suffix
}

func takeTelegramText(value string, limit int) string {
	result := make([]rune, 0, len([]rune(value)))
	length := 0
	for _, character := range value {
		characterLength := utf16.RuneLen(character)
		if length+characterLength > limit {
			break
		}
		result = append(result, character)
		length += characterLength
	}
	return string(result)
}

func telegramTextLength(value string) int {
	length := 0
	for _, character := range value {
		length += utf16.RuneLen(character)
	}
	return length
}
