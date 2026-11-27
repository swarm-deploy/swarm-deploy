package actions

import (
	"fmt"
	"net/url"
	"strings"
)

const languageRussian = "ru"

// Action is a transport-neutral notification link.
type Action struct {
	// Text is the localized action label.
	Text string `json:"text"`
	// URL is the absolute action destination.
	URL string `json:"url"`
}

// Builder creates transport-neutral actions for notification payloads.
type Builder struct {
	origin   *url.URL
	basePath string
	language string
}

// NewBuilder creates an action builder. An empty origin disables actions.
func NewBuilder(origin, basePath, language string) (Builder, error) {
	if origin == "" {
		return Builder{language: language}, nil
	}
	parsed, err := url.Parse(origin)
	if err != nil {
		return Builder{}, fmt.Errorf("parse notification origin: %w", err)
	}
	return Builder{origin: parsed, basePath: basePath, language: language}, nil
}

// Deployment returns the action that opens a deployment in the web interface.
func (b Builder) Deployment(deploymentID string) []Action {
	if b.origin == nil || deploymentID == "" {
		return nil
	}
	query := url.Values{"deployment_id": []string{deploymentID}}
	return []Action{{
		Text: b.text("Open deployment", "Открыть деплоймент"), URL: b.buildURL("/deployments", query),
	}}
}

// Alert returns the action that opens an alert in the web interface.
func (b Builder) Alert(alertID string) []Action {
	if b.origin == nil || alertID == "" {
		return nil
	}
	query := url.Values{"tab": []string{"alerts"}, "alert_id": []string{alertID}}
	return []Action{{Text: b.text("Open alert", "Открыть алерт"), URL: b.buildURL("/events", query)}}
}

func (b Builder) buildURL(route string, query url.Values) string {
	target := *b.origin
	if b.basePath == "/" {
		target.Path = route
	} else {
		target.Path = strings.TrimSuffix(b.basePath, "/") + route
	}
	target.RawQuery = query.Encode()
	return target.String()
}

func (b Builder) text(english, russian string) string {
	if b.language == languageRussian {
		return russian
	}
	return english
}
