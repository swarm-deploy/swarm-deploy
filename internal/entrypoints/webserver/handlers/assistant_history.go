package handlers

import (
	"encoding/json"
	"net/http"
	"strings"
)

func (h *handler) AssistantChatsHTTP(w http.ResponseWriter, r *http.Request) {
	chats, err := h.assistant.ListChats(r.Context())
	if err != nil {
		http.Error(w, "failed to list assistant chats", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err = json.NewEncoder(w).Encode(map[string]any{"chats": chats}); err != nil {
		http.Error(w, "failed to encode assistant chats", http.StatusInternalServerError)
	}
}

func (h *handler) AssistantChatHistoryHTTP(w http.ResponseWriter, r *http.Request) {
	conversationID := strings.TrimSpace(r.PathValue("conversationID"))
	if conversationID == "" {
		http.Error(w, "conversation id is required", http.StatusBadRequest)
		return
	}

	chat, ok, err := h.assistant.GetChat(r.Context(), conversationID)
	if err != nil {
		http.Error(w, "failed to load assistant chat", http.StatusInternalServerError)
		return
	}
	if !ok {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err = json.NewEncoder(w).Encode(chat); err != nil {
		http.Error(w, "failed to encode assistant chat", http.StatusInternalServerError)
	}
}
