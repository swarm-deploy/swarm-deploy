package legacyimport

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	alertmodel "github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement/model"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/assistant/conversation"
)

type identities map[string]bool

func (seen identities) add(kind, id string) error {
	if id == "" || seen[kind+":"+id] {
		return fmt.Errorf("missing or duplicate legacy %s identity", kind)
	}
	seen[kind+":"+id] = true
	return nil
}

func (s snapshot) validate() error {
	seen := identities{}
	validators := []func(identities) error{
		s.validateHistory, s.validateAlerts, s.validateResources, s.validateRecommendations,
	}
	for _, validate := range validators {
		if err := validate(seen); err != nil {
			return err
		}
	}
	return nil
}

func (s snapshot) validateHistory(seen identities) error {
	for _, e := range s.history {
		if e.ID != "" {
			if err := seen.add("event", e.ID); err != nil {
				return err
			}
		}
		if e.CreatedAt.IsZero() || e.Type.Name() == "" {
			return fmt.Errorf("invalid legacy event timestamp or type")
		}
	}
	return nil
}

func (s snapshot) validateAlerts(seen identities) error {
	for _, a := range s.alerts {
		if err := seen.add("alert", a.ID); err != nil {
			return err
		}
		if a.Fingerprint == "" || a.OpenedAt.IsZero() || a.UpdatedAt.IsZero() {
			return fmt.Errorf("invalid legacy alert")
		}
		switch a.Status {
		case alertmodel.AlertStatusOpen:
			if err := seen.add("open fingerprint", a.Fingerprint); err != nil {
				return err
			}
		case alertmodel.AlertStatusResolved:
		default:
			return fmt.Errorf("invalid legacy alert status")
		}
	}
	return nil
}

func (s snapshot) validateResources(seen identities) error {
	for _, n := range s.nodes {
		if err := seen.add("node", n.ID); err != nil {
			return err
		}
	}
	for _, secret := range s.secrets {
		if err := seen.add("secret", secret.ID); err != nil {
			return err
		}
		if err := seen.add("secret name", secret.Name); err != nil {
			return err
		}
	}
	for _, service := range s.services {
		if err := seen.add("service", service.Stack+"\x00"+service.Name); err != nil {
			return err
		}
	}
	return nil
}

func (s snapshot) validateRecommendations(seen identities) error {
	for _, r := range s.recommendations {
		if r.Subject.Stack == "" || r.Type == "" {
			return fmt.Errorf("invalid legacy recommendation")
		}
		if err := seen.add("recommendation", r.ID()); err != nil {
			return err
		}
	}
	return nil
}

func readChats(dir string) ([]conversation.Chat, error) {
	var index struct {
		// Chats preserves the legacy index contract.
		Chats []conversation.ChatSummary `json:"chats"`
	}
	if err := readJSON(dir, "index.json", &index); err != nil {
		return nil, err
	}
	files, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	chats := []conversation.Chat{}
	byID := map[string]conversation.Chat{}
	for _, file := range files {
		if file.IsDir() || file.Name() == "index.json" || !strings.HasSuffix(file.Name(), ".json") {
			continue
		}
		chat, readErr := readChat(dir, file.Name())
		if readErr != nil {
			return nil, readErr
		}
		byID[chat.ID] = chat
		chats = append(chats, chat)
	}
	if err = validateChatIndex(index.Chats, byID); err != nil {
		return nil, err
	}
	return chats, nil
}

func readChat(dir, name string) (conversation.Chat, error) {
	payload, readErr := os.ReadFile(filepath.Join(dir, name))
	if readErr != nil {
		return conversation.Chat{}, readErr
	}
	var chat conversation.Chat
	if decodeErr := json.Unmarshal(payload, &chat); decodeErr != nil {
		return conversation.Chat{}, fmt.Errorf("decode assistant chat %s: %w", name, decodeErr)
	}
	sum := sha256.Sum256([]byte(chat.ID))
	if chat.ID == "" || name != hex.EncodeToString(sum[:])+".json" ||
		chat.CreatedAt.IsZero() || chat.UpdatedAt.IsZero() {
		return conversation.Chat{}, fmt.Errorf("invalid assistant chat identity, filename or timestamp: %s", name)
	}
	return chat, nil
}

func validateChatIndex(summaries []conversation.ChatSummary, byID map[string]conversation.Chat) error {
	seen := map[string]bool{}
	for _, meta := range summaries {
		chat, ok := byID[meta.ID]
		if !ok || seen[meta.ID] {
			return fmt.Errorf("missing or duplicate indexed assistant chat")
		}
		seen[meta.ID] = true
		if meta.Title != chat.Title || !meta.CreatedAt.Equal(chat.CreatedAt) ||
			!meta.UpdatedAt.Equal(chat.UpdatedAt) || !reflect.DeepEqual(meta.Usage, chat.Usage) {
			return fmt.Errorf("assistant chat index disagrees with chat %q", meta.ID)
		}
	}
	return nil
}
