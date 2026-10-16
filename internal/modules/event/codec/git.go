package codec

import (
	"encoding/json"
	"errors"
	"net/url"
	"strings"

	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
)

type gitPullFailedPayload struct {
	Repository    string                    `json:"repository"`
	Category      events.GitFailureCategory `json:"category"`
	Description   string                    `json:"description"`
	TriggerReason string                    `json:"triggerReason,omitempty"`
}

type gitPullSucceededPayload struct {
	Repository    string `json:"repository"`
	TriggerReason string `json:"triggerReason,omitempty"`
}

type configurationChangedPayload struct {
	PreviousRepository string `json:"previousRepository,omitempty"`
	Repository         string `json:"repository,omitempty"`
}

func encodeConfigurationChanged(event *events.ConfigurationChanged) ([]byte, error) {
	if err := validateRepositoryTransition(event.PreviousRepository, event.Repository); err != nil {
		return nil, err
	}
	return json.Marshal(configurationChangedPayload{
		PreviousRepository: event.PreviousRepository, Repository: event.Repository,
	})
}

func decodeConfigurationChanged(payload []byte) (events.Event, error) {
	var value configurationChangedPayload
	if err := decode(payload, &value); err != nil {
		return nil, err
	}
	if err := validateRepositoryTransition(value.PreviousRepository, value.Repository); err != nil {
		return nil, err
	}
	return &events.ConfigurationChanged{
		PreviousRepository: value.PreviousRepository, Repository: value.Repository,
	}, nil
}

func validateRepositoryTransition(previous, current string) error {
	if previous == "" && current == "" {
		return nil
	}
	if previous == "" || current == "" || previous == current {
		return errors.New("invalid git repository configuration transition")
	}
	if err := validateRepositoryIdentifier(previous); err != nil {
		return err
	}
	return validateRepositoryIdentifier(current)
}

func encodeGitPullFailed(event *events.GitPullFailed) ([]byte, error) {
	description, ok := safeGitFailureDescription(event.Category)
	if !ok {
		return nil, errors.New("unsupported Git failure category")
	}
	// Only retain allowlisted diagnostics from the Git classifier. Raw transport
	// errors may include credentials and must never reach the durable outbox.
	if isAllowedGitFailureDescription(event.Category, event.Description) {
		description = event.Description
	}
	if err := validateRepositoryIdentifier(event.Repository); err != nil {
		return nil, err
	}
	return json.Marshal(gitPullFailedPayload{
		Repository: event.Repository, Category: event.Category, Description: description,
		TriggerReason: event.TriggerReason,
	})
}

func safeGitFailureDescription(category events.GitFailureCategory) (string, bool) {
	switch category {
	case events.GitFailureAuthentication:
		return "Git repository authentication failed", true
	case events.GitFailureAccessDenied:
		return "Access to Git repository was denied", true
	case events.GitFailureRepositoryNotFound:
		return "Git repository was not found", true
	case events.GitFailureNetwork:
		return "Git repository is temporarily unreachable", true
	case events.GitFailureSSHHostVerification:
		return "SSH host verification failed", true
	case events.GitFailureOther:
		return "Git pull failed", true
	default:
		return "", false
	}
}

// isAllowedGitFailureDescription validates a category-specific, non-sensitive
// diagnostic. The allowlist is intentionally static: never accept raw errors.
func isAllowedGitFailureDescription(category events.GitFailureCategory, description string) bool {
	fallback, ok := safeGitFailureDescription(category)
	if !ok {
		return false
	}
	if description == fallback {
		return true
	}
	if category != events.GitFailureNetwork {
		return false
	}
	switch description {
	case "Git repository DNS lookup failed",
		"Git repository connection timed out",
		"Git repository connection refused",
		"Git repository host unreachable",
		"Git repository network unreachable",
		"Git remote returned HTTP 502 (Bad Gateway)",
		"Git remote returned HTTP 503 (Service Unavailable)",
		"Git remote returned HTTP 504 (Gateway Timeout)":
		return true
	default:
		return false
	}
}

func validateRepositoryIdentifier(repository string) error {
	if strings.TrimSpace(repository) == "" {
		return errors.New("git repository identifier is required")
	}
	parsed, err := url.Parse(repository)
	if err != nil {
		return errors.New("invalid git repository identifier")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("git repository identifier contains unsafe URL components")
	}
	return nil
}

func decodeGitPullFailed(payload []byte) (events.Event, error) {
	var value gitPullFailedPayload
	if err := decode(payload, &value); err != nil {
		return nil, err
	}
	if !isAllowedGitFailureDescription(value.Category, value.Description) {
		return nil, errors.New("invalid Git failure diagnostic")
	}
	if err := validateRepositoryIdentifier(value.Repository); err != nil {
		return nil, err
	}
	return &events.GitPullFailed{
		Repository: value.Repository, Category: value.Category, Description: value.Description,
		TriggerReason: value.TriggerReason,
	}, nil
}

func decodeGitPullSucceeded(payload []byte) (events.Event, error) {
	var value gitPullSucceededPayload
	if err := decode(payload, &value); err != nil {
		return nil, err
	}
	if err := validateRepositoryIdentifier(value.Repository); err != nil {
		return nil, err
	}
	return &events.GitPullSucceeded{Repository: value.Repository, TriggerReason: value.TriggerReason}, nil
}
