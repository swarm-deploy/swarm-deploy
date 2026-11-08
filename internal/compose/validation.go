package compose

import (
	"fmt"

	"github.com/distribution/reference"
)

// ValidateEffective checks image references in the effective Compose definition.
// The path is optional when the definition is supplied without a source file.
func ValidateEffective(desired Compose, path string) error {
	var issues []ValidationIssue
	for _, service := range desired.Services {
		issue := ValidationIssue{ResourceType: validationResourceService, ResourceName: service.Name, Field: "image"}
		if service.Image == "" {
			issue.Code = IssueCodeRequired
			issue.Message = "required"
		} else if _, err := reference.ParseNormalizedNamed(service.Image); err != nil {
			issue.Code = IssueCodeInvalidValue
			issue.Message = fmt.Sprintf("invalid reference %q", service.Image)
		} else {
			continue
		}
		issues = append(issues, issue)
	}
	if len(issues) != 0 {
		return &ValidateComposeError{FilePath: path, Issues: issues}
	}
	return nil
}
