package notifications

import (
	_ "embed"
	"strings"
)

//go:embed default.gotmpl
var defaultTemplate string

// DefaultTemplate returns the standard reconciliation notification template.
func DefaultTemplate() string { return strings.TrimSpace(defaultTemplate) }
