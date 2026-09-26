package guard

import "regexp"

type InjectionChecker struct {
}

var injectionPatterns = []*regexp.Regexp{
	regexp.MustCompile(
		`(?i)\b(?:show|reveal|print|repeat)\s+(?:your\s+)?(?:(?:system|hidden|initial|developer|private)\s+)?(?:prompt|instructions?)\b`,
	),
	regexp.MustCompile(
		`(?i)\bignore\s+(?:all\s+)?(?:previous|prior|above)\s+instructions?\b`,
	),
	regexp.MustCompile(
		`(?i)(?:дай|покажи|раскрой|выведи|повтори)\s+(?:(?:мне\s+)?(?:свой|твой|системн(?:ый|ые)|скрыт(?:ый|ые))\s+)*(?:промпт|инструкци(?:ю|и))`,
	),
	regexp.MustCompile(
		`(?i)какие\s+(?:(?:системные|скрытые)\s+)?инструкции\s+тебе\s+(?:дали|задали)`,
	),
	regexp.MustCompile(
		`(?i)\b(system prompt|developer mode|developer message|dan mode|jailbreak|режим разработчика)\b`,
	),
	regexp.MustCompile(
		`(?i)\b(you are now|теперь ты|ты должен)\b.*\b(forced to|required to|allowed to|обязан|должен)\b`,
	),
	regexp.MustCompile(
		`(?i)(системный промпт|системное сообщение|скрытую инструкцию|сообщение разработчика|скрытый промпт|системный запрос)`,
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
