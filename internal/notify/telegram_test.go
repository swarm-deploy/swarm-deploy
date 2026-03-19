package notify

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestTelegramNotifierSendsThreadAndRenderedTemplate(t *testing.T) {
	var received map[string]any

	notifier, err := NewTelegramNotifier(
		"ops",
		"TOKEN",
		"-100123",
		TelegramOptions{
			ChatThreadID: 42,
			Message:      "stack={{.stack_name}} image={{.image.full_name}}:{{.image.version}} success={{.success}}",
			APIBaseURL:   "https://telegram.invalid",
		},
	)
	if err != nil {
		t.Fatalf("build notifier: %v", err)
	}
	notifier.client = &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if r.Method != http.MethodPost {
				t.Fatalf("unexpected method: %s", r.Method)
			}
			if r.URL.Path != "/botTOKEN/sendMessage" {
				t.Fatalf("unexpected path: %s", r.URL.Path)
			}

			defer r.Body.Close()
			decodeErr := json.NewDecoder(r.Body).Decode(&received)
			if decodeErr != nil {
				t.Fatalf("decode request body: %v", decodeErr)
			}

			return &http.Response{
				StatusCode: http.StatusOK,
				Status:     "200 OK",
				Body:       io.NopCloser(strings.NewReader("ok")),
				Header:     make(http.Header),
			}, nil
		}),
	}

	err = notifier.Notify(context.Background(), Event{
		Status:    "success",
		StackName: "app",
		Service:   "api",
		Image: Image{
			FullName: "ghcr.io/acme/api",
			Version:  "1.2.3",
		},
		Timestamp: time.Now(),
	})
	if err != nil {
		t.Fatalf("notify: %v", err)
	}

	if received["chat_id"] != "-100123" {
		t.Fatalf("unexpected chat_id: %#v", received["chat_id"])
	}
	threadIDRaw, ok := received["message_thread_id"].(float64)
	if !ok {
		t.Fatalf("message_thread_id has unexpected type: %#v", received["message_thread_id"])
	}
	if int64(threadIDRaw) != 42 {
		t.Fatalf("unexpected message_thread_id: %#v", received["message_thread_id"])
	}
	if received["text"] != "stack=app image=ghcr.io/acme/api:1.2.3 success=true" {
		t.Fatalf("unexpected text: %#v", received["text"])
	}
}

func TestTelegramNotifierInvalidTemplate(t *testing.T) {
	_, err := NewTelegramNotifier(
		"ops",
		"TOKEN",
		"-100123",
		TelegramOptions{
			Message: "{{ if }}",
		},
	)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}
