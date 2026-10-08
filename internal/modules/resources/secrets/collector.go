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
	defaultReconnectDelay  = 5 * time.Second
	defaultReconcilePeriod = time.Hour

	descriptionLabel       = "description"
	externalPathLabel      = "external_path"
	externalVersionIDLabel = "external_version_id"
	managedLabel           = "cloud-secrets.secret.managed"
)

// Collector keeps the persisted secret metadata snapshot synchronized with Docker.
type Collector struct {
	manager swarm.SecretManager
	store   modelstore.Store

	debounceDelay   time.Duration
	reconnectDelay  time.Duration
	reconcilePeriod time.Duration
}

// NewCollector creates a Docker secret metadata collector.
func NewCollector(manager swarm.SecretManager, store modelstore.Store) *Collector {
	return &Collector{
		manager:         manager,
		store:           store,
		debounceDelay:   defaultDebounceDelay,
		reconnectDelay:  defaultReconnectDelay,
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

	refreshAfterConnect := false
	for {
		err := c.watchOnce(ctx, reconcileTicker.C, refreshAfterConnect)
		if err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return nil
		}

		refreshAfterConnect = true
		slog.WarnContext(ctx, "[secrets] watch stream failed", slog.Any("err", err))
		if !waitFor(ctx, c.reconnectDelay) {
			return nil
		}
	}
}

func (c *Collector) refresh(ctx context.Context) error {
	dockerSecrets, err := c.manager.List(ctx, swarm.ListSecretsFilter{})
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

func (c *Collector) watchOnce(
	ctx context.Context,
	reconcile <-chan time.Time,
	refreshAfterConnect bool,
) error {
	eventsCh, errorsCh, err := c.manager.Watch(ctx)
	if err != nil {
		return fmt.Errorf("subscribe docker secret events: %w", err)
	}
	if refreshAfterConnect {
		if refreshErr := c.refresh(ctx); refreshErr != nil {
			slog.WarnContext(ctx, "[secrets] refresh after reconnect failed", slog.Any("err", refreshErr))
		}
	}

	debouncer := newRefreshDebouncer(c.debounceDelay)
	defer debouncer.stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case _, ok := <-eventsCh:
			if !ok {
				return errors.New("docker secret events channel closed")
			}
			debouncer.trigger()
		case <-debouncer.wait():
			debouncer.markFired()
			c.refreshWithWarning(ctx, "[secrets] refresh after event failed")
		case <-reconcile:
			c.refreshWithWarning(ctx, "[secrets] periodic refresh failed")
		case watchErr, ok := <-errorsCh:
			if !ok {
				return errors.New("docker secret events errors channel closed")
			}
			if watchErr != nil {
				return fmt.Errorf("watch docker secret events: %w", watchErr)
			}
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
		Description:       labels[descriptionLabel],
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

func waitFor(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
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
