package compose

import (
	"fmt"

	"github.com/distribution/reference"
)

// ValidateEffective checks images and Init Job mounts in the effective Compose definition.
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
		}
		if issue.Code != "" {
			issues = append(issues, issue)
		}
		for i, job := range service.InitJobs {
			if err := job.ValidateObjectRefs(); err != nil {
				issues = append(issues, ValidationIssue{
					ResourceType: validationResourceService,
					ResourceName: service.Name,
					Field:        fmt.Sprintf("x-init-deploy-jobs[%d]", i),
					Code:         IssueCodeInvalidValue,
					Message:      err.Error(),
				})
			}
		}
	}
	if len(issues) != 0 {
		return &ValidateComposeError{FilePath: path, Issues: issues}
	}
	return nil
}
