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

func encodeGitPullFailed(event *events.GitPullFailed) ([]byte, error) {
	description, ok := safeGitFailureDescription(event.Category)
	if !ok {
		return nil, errors.New("unsupported Git failure category")
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

func validateRepositoryIdentifier(repository string) error {
	if strings.TrimSpace(repository) == "" {
		return errors.New("git repository identifier is required")
	}
	parsed, err := url.Parse(repository)
	if err == nil && (parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "") {
		return errors.New("git repository identifier contains unsafe URL components")
	}
	return nil
}

func decodeGitPullFailed(payload []byte) (events.Event, error) {
	var value gitPullFailedPayload
	if err := decode(payload, &value); err != nil {
		return nil, err
	}
	description, ok := safeGitFailureDescription(value.Category)
	if !ok || value.Description != description {
		return nil, errors.New("invalid Git failure diagnostic")
	}
	if err := validateRepositoryIdentifier(value.Repository); err != nil {
		return nil, err
	}
	return &events.GitPullFailed{
		Repository: value.Repository, Category: value.Category, Description: description,
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
