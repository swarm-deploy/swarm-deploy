package notifications

import (
	_ "embed"
	"strings"
)

var (
	//go:embed success.gotmpl
	successTemplate string
	//go:embed failed.gotmpl
	failedTemplate string
)

// SuccessTemplate returns the standard deployment-success template.
func SuccessTemplate() string { return strings.TrimSpace(successTemplate) }

// FailedTemplate returns the standard deployment-failure template.
func FailedTemplate() string { return strings.TrimSpace(failedTemplate) }
