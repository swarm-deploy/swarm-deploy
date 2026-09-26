package events

type AssistantPromptInjectionDetector string

const (
	AssistantPromptInjectionDetectorRegexp AssistantPromptInjectionDetector = "regexp"
	AssistantPromptInjectionDetectorModel  AssistantPromptInjectionDetector = "model"
)

type AssistantPromptInjectionDetected struct {
	ChatID   string
	Detector AssistantPromptInjectionDetector
	Username string
}

func (m *AssistantPromptInjectionDetected) Type() Type {
	return TypeAssistantPromptInjectionDetected
}

func (m *AssistantPromptInjectionDetected) Message() string {
	return "Detected prompt injection"
}

func (m *AssistantPromptInjectionDetected) Details() map[string]string {
	details := map[string]string{
		"detector": string(m.Detector),
	}

	if m.ChatID != "" {
		details["chat_id"] = m.ChatID
	}

	if m.Username != "" {
		details["username"] = m.Username
	}

	return details
}

func (m *AssistantPromptInjectionDetected) WithUsername(username string) Event {
	return &AssistantPromptInjectionDetected{
		ChatID:   m.ChatID,
		Detector: m.Detector,
		Username: username,
	}
}
