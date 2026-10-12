package notifications

import (
	_ "embed"
	"strings"
)

//go:embed default.gotmpl
var defaultTemplate string

// DefaultTemplate returns the standard node notification template.
func DefaultTemplate() string { return strings.TrimSpace(defaultTemplate) }
