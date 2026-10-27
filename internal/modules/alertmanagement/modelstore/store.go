package modelstore

import (
	"context"
	"errors"

	"github.com/swarm-deploy/swarm-deploy/internal/modules/alertmanagement/model"
)

const (
	// MaxStoredAlerts is the maximum persisted alert count when resolved alerts can be pruned.
	MaxStoredAlerts = 100
)

var (
	ErrAlertNotFound        = errors.New("alert not found")
	ErrAlertOpenedAtChanged = errors.New("alert opened at cannot be changed")
	ErrOpenAlertExists      = errors.New("open alert already exists for fingerprint")
)

// ListFilter selects alerts returned by a store.
type ListFilter struct {
	// Status selects alerts in one lifecycle state; empty returns all alerts.
	Status model.AlertStatus
	// Kind selects alerts of one kind; empty returns every kind.
	Kind model.AlertKind
	// ResourceType selects alerts for one resource kind; empty returns every kind.
	ResourceType model.ResourceType
	// ResourceID selects alerts for one resource identifier; empty returns every resource.
	ResourceID string
	// DeploymentID selects alerts correlated with one desired-state deployment; empty returns every deployment.
	DeploymentID string
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
	// IncrementFailureStreak records another consecutive failure and returns the new count.
	IncrementFailureStreak(ctx context.Context, fingerprint string) (uint64, error)
	// ResetFailureStreak clears consecutive failures after recovery.
	ResetFailureStreak(ctx context.Context, fingerprint string) error
	// ResetFailureStreaksExcept clears stack counters for resources absent from the desired configuration.
	ResetFailureStreaksExcept(ctx context.Context, fingerprints []string) error
}
