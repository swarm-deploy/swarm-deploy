package notifications

import (
	_ "embed"
	"strings"
)

var (
	//go:embed default.gotmpl
	defaultTemplate string
	//go:embed success.gotmpl
	successTemplate string
)

// DefaultTemplate returns the standard message-based event notification template.
func DefaultTemplate() string { return strings.TrimSpace(defaultTemplate) }

// SuccessTemplate returns the standard deployment-success template.
func SuccessTemplate() string { return strings.TrimSpace(successTemplate) }
