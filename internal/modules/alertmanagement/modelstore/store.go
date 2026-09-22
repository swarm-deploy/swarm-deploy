package modelstore

import (
	"context"
	"errors"

	"github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement/model"
)

var (
	ErrAlertNotFound   = errors.New("alert not found")
	ErrOpenAlertExists = errors.New("open alert already exists for fingerprint")
)

// ListFilter selects alerts returned by a store.
type ListFilter struct {
	// Status selects alerts in one lifecycle state; empty returns all alerts.
	Status model.AlertStatus
	// Limit limits the number of returned alerts; zero means unlimited.
	Limit int
}

// Store persists alerts and maintains the open-alert uniqueness invariant.
type Store interface {
	// Create persists a new alert.
	Create(ctx context.Context, alert model.Alert) error
	// Update replaces an existing alert.
	Update(ctx context.Context, alert model.Alert) error
	// Get returns an alert by incident ID.
	Get(ctx context.Context, id string) (model.Alert, error)
	// FindOpenByFingerprint returns the active incident for a fingerprint.
	FindOpenByFingerprint(ctx context.Context, fingerprint string) (model.Alert, error)
	// List returns alerts matching the filter.
	List(ctx context.Context, filter ListFilter) ([]model.Alert, error)
}
