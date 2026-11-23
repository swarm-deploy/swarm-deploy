package template

import (
	"fmt"
	"net/url"
	"strings"
	"unicode"
)

// ButtonSpec configures a URL button with two notification templates.
type ButtonSpec struct {
	// Text is the template for the button label.
	Text string `yaml:"text"`
	// URL is the template for the button destination.
	URL string `yaml:"url"`
}

// Button is a rendered URL button.
type Button struct {
	// Text is the button label.
	Text string `json:"text"`
	// URL is the button destination.
	URL string `json:"url"`
}

// ButtonTemplates holds parsed templates in configuration order.
type ButtonTemplates []buttonTemplate

type buttonTemplate struct {
	text *Renderer
	url  *Renderer
}

// NewButtons parses the label and destination of every configured button.
func NewButtons(specs []ButtonSpec) (ButtonTemplates, error) {
	var buttons ButtonTemplates
	for i, spec := range specs {
		text, err := parseButtonField(spec.Text)
		if err != nil {
			return nil, fmt.Errorf("buttons[%d].text: %w", i, err)
		}
		address, err := parseButtonField(spec.URL)
		if err != nil {
			return nil, fmt.Errorf("buttons[%d].url: %w", i, err)
		}
		buttons = append(buttons, buttonTemplate{text: text, url: address})
	}
	return buttons, nil
}

func parseButtonField(source string) (*Renderer, error) {
	if strings.TrimSpace(source) == "" {
		return nil, fmt.Errorf("template is required")
	}
	return New(source)
}

// Validate checks both templates against a representative notification payload.
func (buttons ButtonTemplates) Validate(payload any) error {
	_, err := buttons.render(payload, true)
	return err
}

// Render returns the buttons with non-empty, valid destinations.
func (buttons ButtonTemplates) Render(payload any) ([]Button, error) {
	return buttons.render(payload, false)
}

func (buttons ButtonTemplates) render(payload any, validate bool) ([]Button, error) {
	var rendered []Button
	for i, tmpl := range buttons {
		address, err := tmpl.url.Render(payload)
		if err != nil {
			return nil, fmt.Errorf("buttons[%d].url: %w", i, err)
		}
		address = strings.TrimSpace(address)
		if address == "" && !validate {
			continue
		}
		label, err := tmpl.text.Render(payload)
		if err != nil {
			return nil, fmt.Errorf("buttons[%d].text: %w", i, err)
		}
		if address == "" {
			continue
		}
		if strings.TrimSpace(label) == "" {
			return nil, fmt.Errorf("buttons[%d].text: rendered label is empty", i)
		}
		if !validButtonURL(address) {
			return nil, fmt.Errorf("buttons[%d].url: must be an absolute HTTP, HTTPS, or tg:// URL", i)
		}
		rendered = append(rendered, Button{Text: label, URL: address})
	}
	return rendered, nil
}

func validButtonURL(address string) bool {
	parsed, err := url.Parse(address)
	if err != nil || parsed.Hostname() == "" || strings.ContainsFunc(address, unicode.IsSpace) {
		return false
	}
	switch parsed.Scheme {
	case "http", "https", "tg":
		return true
	default:
		return false
	}
}
