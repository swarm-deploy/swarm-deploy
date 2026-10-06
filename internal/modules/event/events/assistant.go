package events

type AssistantPromptInjectionDetector string

const (
	AssistantPromptInjectionDetectorRegexp AssistantPromptInjectionDetector = "regexp"
	AssistantPromptInjectionDetectorModel  AssistantPromptInjectionDetector = "model"
)

type AssistantPromptInjectionDetected struct {
	Prompt   string
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

	if m.Prompt != "" {
		details["prompt"] = m.Prompt
	}

	if m.Username != "" {
		details["username"] = m.Username
	}

	return details
}

func (m *AssistantPromptInjectionDetected) WithUsername(username string) Event {
	return &AssistantPromptInjectionDetected{
		Prompt:   m.Prompt,
		Detector: m.Detector,
		Username: username,
	}
}
