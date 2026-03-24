package events

type AssistantPromptInjectionDetected struct {
	Prompt string
}

func (m *AssistantPromptInjectionDetected) Type() Type {
	return TypeAssistantPromptInjectionDetected
}

func (m *AssistantPromptInjectionDetected) Message() string {
	return "Detected prompt injection"
}

func (m *AssistantPromptInjectionDetected) Details() map[string]string {
	details := map[string]string{}

	if m.Prompt != "" {
		details["prompt"] = m.Prompt
	}

	return details
}
