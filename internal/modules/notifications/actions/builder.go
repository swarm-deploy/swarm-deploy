package actions

import (
	"embed"
	"fmt"
	"net/url"
	"strings"

	sharedlanguage "github.com/swarm-deploy/swarm-deploy/internal/shared/language"
)

//go:embed messages.en.json messages.ru.json
var catalogs embed.FS

// Action is a transport-neutral notification link.
type Action struct {
	// Text is the localized action label.
	Text string `json:"text"`
	// URL is the absolute action destination.
	URL string `json:"url"`
}

// Builder creates transport-neutral actions for notification payloads.
type Builder struct {
	origin    *url.URL
	basePath  string
	localizer *sharedlanguage.Localizer
}

// NewBuilder creates an action builder. An empty origin disables actions.
func NewBuilder(origin, basePath, language string) (Builder, error) {
	localizer, err := sharedlanguage.New(language, catalogs, "messages.en.json", "messages.ru.json")
	if err != nil {
		return Builder{}, fmt.Errorf("load notification action texts: %w", err)
	}
	if origin == "" {
		return Builder{localizer: localizer}, nil
	}
	parsed, err := url.Parse(origin)
	if err != nil {
		return Builder{}, fmt.Errorf("parse notification origin: %w", err)
	}
	return Builder{origin: parsed, basePath: basePath, localizer: localizer}, nil
}

// Deployment returns the action that opens a deployment in the web interface.
func (b Builder) Deployment(deploymentID string) []Action {
	if b.origin == nil || deploymentID == "" {
		return nil
	}
	query := url.Values{"deployment_id": []string{deploymentID}}
	return []Action{{
		Text: b.localizer.Text("action.deployment"), URL: b.buildURL("/deployments", query),
	}}
}

// Alert returns the action that opens an alert in the web interface.
func (b Builder) Alert(alertID string) []Action {
	if b.origin == nil || alertID == "" {
		return nil
	}
	query := url.Values{"tab": []string{"alerts"}, "alert_id": []string{alertID}}
	return []Action{{Text: b.localizer.Text("action.alert"), URL: b.buildURL("/events", query)}}
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
