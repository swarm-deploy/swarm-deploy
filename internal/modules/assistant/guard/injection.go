package guard

import "regexp"

type InjectionChecker struct {
}

// Go regular expressions treat \b as an ASCII-only word boundary, so it never matches next to
// Cyrillic letters. Russian patterns use explicit letter boundaries instead.
//
//nolint:lll // Regular expressions are kept whole so their security-sensitive meaning remains auditable.
var injectionPatterns = []*regexp.Regexp{
	regexp.MustCompile(
		`(?i)\b(?:show|reveal|print|repeat)\s+(?:your\s+)?(?:(?:system|hidden|initial|developer|private)\s+)?(?:prompt|instructions?)\b`,
	),
	regexp.MustCompile(
		`(?i)\b(?:ignore|disregard)\s+(?:all\s+)?(?:previous|prior|above)\s+instructions?\b`,
	),
	// A bare "инструкция" is an ordinary operational word ("покажи инструкцию по деплою"),
	// so instructions match only with an ownership or hidden/system qualifier.
	regexp.MustCompile(
		`(?i)(?:^|\P{L})(?:дай|покажи|раскрой|выведи|повтори)\s+(?:мне\s+)?(?:(?:(?:свой|свои|твой|твои|системн\p{L}*|скрыт\p{L}*)\s+)+(?:промпт|инструкци)\p{L}*|промпт\p{L}*)`,
	),
	regexp.MustCompile(
		`(?i)инструкци\p{L}*,?\s+(?:которые\s+)?тебе\s+(?:дали|задали|выдали)`,
	),
	regexp.MustCompile(
		`(?i)(?:^|\P{L})(?:игнорируй|проигнорируй|забудь)\s+(?:все\s+)?(?:предыдущие|прошлые|прежние|свои|системные)\s+инструкци\p{L}*`,
	),
	regexp.MustCompile(
		`(?i)\b(system prompt|developer mode|developer message|dan mode|jailbreak)\b`,
	),
	regexp.MustCompile(
		`(?i)\byou are now\b.*\b(?:forced|required|allowed)\s+to\b`,
	),
	// "теперь ты должен …" is an ordinary follow-up imperative; only role reassignment is matched.
	regexp.MustCompile(
		`(?i)(?:^|\P{L})теперь\s+ты\s+(?:[-—]\s*)?(?:не\s+)?(?:ассистент|бот|модель|ии|ai|dan|другой|другая|новый|новая|свободн\p{L}*|без\s+ограничений)(?:$|\P{L})`,
	),
	regexp.MustCompile(
		`(?i)(системный промпт|системное сообщение|скрытую инструкцию|сообщение разработчика|скрытый промпт|системный запрос|режим\p{L}*\s+разработчика)`,
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
