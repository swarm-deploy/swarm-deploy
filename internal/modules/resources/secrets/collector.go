package secrets

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/swarm-deploy/swarm-deploy/internal/modules/resources/secrets/model"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/resources/secrets/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

const (
	defaultDebounceDelay   = 250 * time.Millisecond
	defaultReconcilePeriod = time.Hour

	externalPathLabel      = "external_path"
	externalVersionIDLabel = "external_version_id"
	managedLabel           = "cloud-secrets.secret.managed"
)

// Collector keeps the persisted secret metadata snapshot synchronized with Docker.
type Collector struct {
	manager      swarm.SecretManager
	store        modelstore.Store
	subscription swarm.EventSubscription[swarm.SecretEvent]

	debounceDelay   time.Duration
	reconcilePeriod time.Duration
}

// NewCollector creates a Docker secret metadata collector.
func NewCollector(
	manager swarm.SecretManager,
	store modelstore.Store,
	subscription swarm.EventSubscription[swarm.SecretEvent],
) *Collector {
	return &Collector{
		manager:         manager,
		store:           store,
		subscription:    subscription,
		debounceDelay:   defaultDebounceDelay,
		reconcilePeriod: defaultReconcilePeriod,
	}
}

// Run performs an initial refresh and watches Docker secret events until cancellation.
func (c *Collector) Run(ctx context.Context) error {
	if err := c.refresh(ctx); err != nil {
		slog.WarnContext(ctx, "[secrets] initial refresh failed", slog.Any("err", err))
	}

	reconcileTicker := time.NewTicker(c.reconcilePeriod)
	defer reconcileTicker.Stop()

	return c.watch(ctx, reconcileTicker.C)
}

func (c *Collector) refresh(ctx context.Context) error {
	dockerSecrets, err := c.manager.List(ctx)
	if err != nil {
		return fmt.Errorf("list docker secrets: %w", err)
	}

	secrets := make([]model.Secret, len(dockerSecrets))
	for index, secret := range dockerSecrets {
		secrets[index] = mapSecret(secret)
	}
	if err = c.store.Replace(ctx, secrets); err != nil {
		return fmt.Errorf("save secret snapshot: %w", err)
	}

	slog.InfoContext(ctx, "[secrets] snapshot refreshed", slog.Int("count", len(secrets)))
	return nil
}

func (c *Collector) watch(ctx context.Context, reconcile <-chan time.Time) error {
	debouncer := newRefreshDebouncer(c.debounceDelay)
	defer debouncer.stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case _, ok := <-c.subscription.Events:
			if !ok {
				return errors.New("docker secret events channel closed")
			}
			debouncer.trigger()
		case _, ok := <-c.subscription.Resync:
			if !ok {
				return errors.New("docker events resync channel closed")
			}
			c.refreshWithWarning(ctx, "[secrets] refresh after stream reconnect failed")
		case <-debouncer.wait():
			debouncer.markFired()
			c.refreshWithWarning(ctx, "[secrets] refresh after event failed")
		case <-reconcile:
			c.refreshWithWarning(ctx, "[secrets] periodic refresh failed")
		}
	}
}

func (c *Collector) refreshWithWarning(ctx context.Context, message string) {
	if err := c.refresh(ctx); err != nil {
		slog.WarnContext(ctx, message, slog.Any("err", err))
	}
}

func mapSecret(secret swarm.Secret) model.Secret {
	labels := cloneLabels(secret.Labels)
	return model.Secret{
		ID:                secret.ID,
		Name:              secret.Name,
		VersionID:         secret.VersionID,
		CreatedAt:         secret.CreatedAt,
		UpdatedAt:         secret.UpdatedAt,
		Driver:            secret.Driver,
		ExternalPath:      labels[externalPathLabel],
		ExternalVersionID: labels[externalVersionIDLabel],
		Managed:           labels[managedLabel] == "true",
		Labels:            labels,
	}
}

func cloneLabels(labels map[string]string) map[string]string {
	if len(labels) == 0 {
		return nil
	}

	cloned := make(map[string]string, len(labels))
	for key, value := range labels {
		cloned[key] = value
	}

	return cloned
}

type refreshDebouncer struct {
	delay   time.Duration
	timer   *time.Timer
	channel <-chan time.Time
}

func newRefreshDebouncer(delay time.Duration) *refreshDebouncer {
	return &refreshDebouncer{delay: delay}
}

func (d *refreshDebouncer) trigger() {
	if d.timer == nil {
		d.timer = time.NewTimer(d.delay)
		d.channel = d.timer.C
		return
	}

	if !d.timer.Stop() {
		select {
		case <-d.timer.C:
		default:
		}
	}
	d.timer.Reset(d.delay)
	d.channel = d.timer.C
}

func (d *refreshDebouncer) wait() <-chan time.Time {
	return d.channel
}

func (d *refreshDebouncer) markFired() {
	d.channel = nil
}

func (d *refreshDebouncer) stop() {
	if d.timer != nil {
		d.timer.Stop()
	}
}
