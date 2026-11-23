// Package template provides the shared notification template engine.
package template

import (
	"bytes"
	"fmt"
	texttemplate "text/template"
	"time"
)

// Renderer is a parsed notification template.
type Renderer struct {
	template *texttemplate.Template
}

// New parses a notification template with the functions available to user templates.
func New(source string) (*Renderer, error) {
	tmpl, err := texttemplate.New("notification").
		Option("missingkey=error").
		Funcs(texttemplate.FuncMap{"date": formatDate}).
		Parse(source)
	if err != nil {
		return nil, fmt.Errorf("parse template: %w", err)
	}

	return &Renderer{template: tmpl}, nil
}

// Render executes the template with a stable notification payload.
func (r *Renderer) Render(payload any) (string, error) {
	var output bytes.Buffer
	if err := r.template.Execute(&output, payload); err != nil {
		return "", fmt.Errorf("execute template: %w", err)
	}

	return output.String(), nil
}

// Validate parses and executes source with the representative payload.
func Validate(source string, payload any) error {
	renderer, err := New(source)
	if err != nil {
		return err
	}
	_, err = renderer.Render(payload)
	return err
}

func formatDate(layout string, value any) (string, error) {
	switch date := value.(type) {
	case time.Time:
		return date.UTC().Format(layout), nil
	case *time.Time:
		if date == nil {
			return "", nil
		}
		return date.UTC().Format(layout), nil
	case nil:
		return "", nil
	default:
		return "", fmt.Errorf("date expects time.Time or *time.Time, got %T", value)
	}
}
