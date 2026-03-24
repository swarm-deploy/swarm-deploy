package guard

import "regexp"

type InjectionChecker struct {
}

var injectionPatterns = []*regexp.Regexp{
	// English
	regexp.MustCompile(`(system prompt|developer message|hidden prompt)`),

	// Russian
	regexp.MustCompile(
		`(системный промпт|системное сообщение|скрытую инструкцию|сообщение разработчика|скрытый промпт|системный запрос)`,
	),
}

func NewInjectionChecker() *InjectionChecker {
	return &InjectionChecker{}
}

// Check the message for injection attempts.
// Returns true if a prompt injection attempt is detected.
func (c *InjectionChecker) Check(message string) bool {
	if message == "" {
		return false
	}

	for _, pattern := range injectionPatterns {
		if pattern.MatchString(message) {
			return true
		}
	}

	return false
}
