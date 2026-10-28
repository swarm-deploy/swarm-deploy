package stackloop

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/artarts36/specw"
	dockerswarm "github.com/docker/docker/api/types/swarm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/labelsdict"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	"go.uber.org/mock/gomock"
)

func TestRotatedResourceCleanerPolicy(t *testing.T) {
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	old := now.Add(-2 * time.Hour)
	recent := now.Add(-30 * time.Minute)

	tests := []struct {
		name            string
		desired         map[string]string
		services        []swarm.StackService
		resources       []swarm.Secret
		keepLast        int
		minAge          time.Duration
		expectedRemoved []string
	}{
		{
			name:    "removes old unused generation",
			desired: map[string]string{"token": "app-token-new"},
			resources: []swarm.Secret{
				managedSecret("old", "app-token-old", "token", old),
				managedSecret("new", "app-token-new", "token", old.Add(time.Minute)),
			},
			keepLast:        1,
			minAge:          time.Hour,
			expectedRemoved: []string{"old"},
		},
		{
			name:    "keeps current desired even when it is not newest",
			desired: map[string]string{"token": "app-token-current"},
			resources: []swarm.Secret{
				managedSecret("current", "app-token-current", "token", old),
				managedSecret("middle", "app-token-middle", "token", old.Add(time.Minute)),
				managedSecret("new", "app-token-new", "token", old.Add(2*time.Minute)),
			},
			keepLast:        1,
			minAge:          time.Hour,
			expectedRemoved: []string{"middle"},
		},
		{
			name:    "keeps referenced generation",
			desired: map[string]string{"token": "app-token-new"},
			services: []swarm.StackService{{
				FullName: "app-api",
				ServiceSpec: dockerswarm.ServiceSpec{TaskTemplate: dockerswarm.TaskSpec{
					ContainerSpec: &dockerswarm.ContainerSpec{Secrets: []*dockerswarm.SecretReference{{SecretID: "old"}}},
				}},
			}},
			resources: []swarm.Secret{
				managedSecret("old", "app-token-old", "token", old),
				managedSecret("new", "app-token-new", "token", old.Add(time.Minute)),
			},
			keepLast: 1,
			minAge:   time.Hour,
		},
		{
			name:    "keeps configured newest generations",
			desired: map[string]string{"token": "app-token-new"},
			resources: []swarm.Secret{
				managedSecret("old", "app-token-old", "token", old),
				managedSecret("middle", "app-token-middle", "token", old.Add(time.Minute)),
				managedSecret("new", "app-token-new", "token", old.Add(2*time.Minute)),
			},
			keepLast:        2,
			minAge:          time.Hour,
			expectedRemoved: []string{"old"},
		},
		{
			name:    "keeps young generation",
			desired: map[string]string{"token": "app-token-new"},
			resources: []swarm.Secret{
				managedSecret("young", "app-token-young", "token", recent),
				managedSecret("new", "app-token-new", "token", now.Add(-time.Minute)),
			},
			keepLast: 1,
			minAge:   time.Hour,
		},
		{
			name:    "ignores external and unmanaged resources",
			desired: map[string]string{"token": "app-token-new"},
			resources: []swarm.Secret{
				func() swarm.Secret {
					secret := managedSecret("external", "app-token-external", "token", old)
					secret.Driver = "vault"
					return secret
				}(),
				{ID: "unmanaged", Name: "app-token-legacy", CreatedAt: old},
				managedSecret("new", "app-token-new", "token", old.Add(time.Minute)),
			},
			keepLast: 1,
			minAge:   time.Hour,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			secrets := swarm.NewMockSecretManager(ctrl)
			configs := swarm.NewMockConfigManager(ctrl)
			for _, id := range tt.expectedRemoved {
				secrets.EXPECT().Remove(gomock.Any(), id).Return(nil)
			}

			cleaner := newRotatedResourceCleaner(secrets, configs, config.SecretRotationCleanupSpec{
				KeepLast: tt.keepLast,
				MinAge:   specw.Duration{Value: tt.minAge},
			})
			cleaner.now = func() time.Time { return now }

			result := cleaner.clean(
				context.Background(), "app", nil, tt.desired, tt.services, nil, tt.resources,
			)

			assert.Equal(t, len(tt.expectedRemoved), result.Removed, "unexpected removed count")
			assert.Zero(t, result.Failed, "expected no failures")
		})
	}
}

func TestRotatedResourceCleanerRemovesAllExpiredGenerationsWhenLogicalResourceIsGone(t *testing.T) {
	ctrl := gomock.NewController(t)
	secrets := swarm.NewMockSecretManager(ctrl)
	configs := swarm.NewMockConfigManager(ctrl)
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	resources := []swarm.Secret{
		managedSecret("old", "app-token-old", "token", now.Add(-3*time.Hour)),
		managedSecret("new", "app-token-new", "token", now.Add(-2*time.Hour)),
	}

	first := secrets.EXPECT().Remove(gomock.Any(), "new").Return(nil)
	secrets.EXPECT().Remove(gomock.Any(), "old").Return(nil).After(first)

	cleaner := newRotatedResourceCleaner(secrets, configs, config.SecretRotationCleanupSpec{
		KeepLast: 2,
		MinAge:   specw.Duration{Value: time.Hour},
	})
	cleaner.now = func() time.Time { return now }

	result := cleaner.clean(context.Background(), "app", nil, map[string]string{}, nil, nil, resources)

	assert.Equal(t, rotatedCleanupResult{Removed: 2}, result, "removed logical resource must not retain keepLast generations")
}

func TestRotatedResourceCleanerContinuesAfterRemoveFailureAndRetriesOnNextRun(t *testing.T) {
	ctrl := gomock.NewController(t)
	secrets := swarm.NewMockSecretManager(ctrl)
	configs := swarm.NewMockConfigManager(ctrl)
	errInUse := errors.New("resource is in use")
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	resources := []swarm.Secret{
		managedSecret("old-a", "app-a-old", "a", now.Add(-3*time.Hour)),
		managedSecret("new-a", "app-a-new", "a", now.Add(-2*time.Hour)),
		managedSecret("old-b", "app-b-old", "b", now.Add(-3*time.Hour)),
		managedSecret("new-b", "app-b-new", "b", now.Add(-2*time.Hour)),
	}

	firstA := secrets.EXPECT().Remove(gomock.Any(), "old-a").Return(errInUse)
	firstB := secrets.EXPECT().Remove(gomock.Any(), "old-b").Return(nil).After(firstA)
	secrets.EXPECT().Remove(gomock.Any(), "old-a").Return(nil).After(firstB)

	cleaner := newRotatedResourceCleaner(secrets, configs, config.SecretRotationCleanupSpec{
		KeepLast: 1,
		MinAge:   specw.Duration{Value: time.Hour},
	})
	cleaner.now = func() time.Time { return now }

	desired := map[string]string{
		"a": "app-a-new",
		"b": "app-b-new",
	}
	first := cleaner.clean(context.Background(), "app", nil, desired, nil, nil, resources)
	assert.Equal(t, rotatedCleanupResult{Removed: 1, Failed: 1}, first, "unexpected first cleanup result")

	secondResources := []swarm.Secret{
		managedSecret("old-a", "app-a-old", "a", now.Add(-3*time.Hour)),
		managedSecret("new-a", "app-a-new", "a", now.Add(-2*time.Hour)),
	}
	second := cleaner.clean(
		context.Background(), "app", nil, map[string]string{"a": "app-a-new"}, nil, nil, secondResources,
	)
	assert.Equal(t, rotatedCleanupResult{Removed: 1}, second, "unexpected retry cleanup result")
}

func TestRotatedResourceCleanerProtectsPreviousSpecReferences(t *testing.T) {
	ctrl := gomock.NewController(t)
	secrets := swarm.NewMockSecretManager(ctrl)
	configs := swarm.NewMockConfigManager(ctrl)
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

	resources := []swarm.Secret{
		managedSecret("rollback", "app-token-rollback", "token", now.Add(-3*time.Hour)),
		managedSecret("middle", "app-token-middle", "token", now.Add(-2*time.Hour)),
		managedSecret("current", "app-token-current", "token", now.Add(-time.Hour)),
	}
	services := []swarm.StackService{{
		FullName: "app-api",
		ServiceSpec: dockerswarm.ServiceSpec{TaskTemplate: dockerswarm.TaskSpec{
			ContainerSpec: &dockerswarm.ContainerSpec{Secrets: []*dockerswarm.SecretReference{{SecretID: "current"}}},
		}},
		PreviousSpec: &dockerswarm.ServiceSpec{TaskTemplate: dockerswarm.TaskSpec{
			ContainerSpec: &dockerswarm.ContainerSpec{Secrets: []*dockerswarm.SecretReference{{SecretID: "rollback"}}},
		}},
	}}

	secrets.EXPECT().Remove(gomock.Any(), "middle").Return(nil)

	cleaner := newRotatedResourceCleaner(secrets, configs, config.SecretRotationCleanupSpec{
		KeepLast: 1,
		MinAge:   specw.Duration{Value: time.Hour},
	})
	cleaner.now = func() time.Time { return now }

	result := cleaner.clean(context.Background(), "app", nil, nil, services, nil, resources)

	assert.Equal(t, rotatedCleanupResult{Removed: 1}, result, "rollback generation must stay protected")
}

func TestRotatedResourceCleanerFailsClosedForIncompletePreviousSpecReference(t *testing.T) {
	ctrl := gomock.NewController(t)
	secrets := swarm.NewMockSecretManager(ctrl)
	configs := swarm.NewMockConfigManager(ctrl)
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

	resources := []swarm.Secret{
		managedSecret("old", "app-token-old", "token", now.Add(-2*time.Hour)),
		managedSecret("new", "app-token-new", "token", now.Add(-time.Hour)),
	}
	services := []swarm.StackService{{
		FullName: "app-api",
		PreviousSpec: &dockerswarm.ServiceSpec{TaskTemplate: dockerswarm.TaskSpec{
			ContainerSpec: &dockerswarm.ContainerSpec{Secrets: []*dockerswarm.SecretReference{{}}},
		}},
	}}

	cleaner := newRotatedResourceCleaner(secrets, configs, config.SecretRotationCleanupSpec{
		KeepLast: 1,
		MinAge:   specw.Duration{Value: time.Hour},
	})
	cleaner.now = func() time.Time { return now }

	result := cleaner.clean(context.Background(), "app", nil, nil, services, nil, resources)

	assert.Equal(t, rotatedCleanupResult{Skipped: 1}, result, "cleanup must fail closed on incomplete rollback reference")
}

func TestRotatedResourceCleanerUsesLiveConfigIDs(t *testing.T) {
	ctrl := gomock.NewController(t)
	secrets := swarm.NewMockSecretManager(ctrl)
	configs := swarm.NewMockConfigManager(ctrl)
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	labels := func(logicalName string) map[string]string {
		return map[string]string{
			labelsdict.StackNamespace:                     "app",
			labelsdict.RotatedResourceManagedLabelKey:     labelsdict.RotatedResourceManagedLabelValue,
			labelsdict.RotatedResourceLogicalNameLabelKey: logicalName,
		}
	}
	liveConfigs := []swarm.Config{
		{ID: "old-used", Name: "app-settings-used", CreatedAt: now.Add(-3 * time.Hour), Labels: labels("settings")},
		{ID: "old-unused", Name: "app-settings-old", CreatedAt: now.Add(-2 * time.Hour), Labels: labels("settings")},
		{ID: "new", Name: "app-settings-new", CreatedAt: now.Add(-time.Hour), Labels: labels("settings")},
	}
	services := []swarm.StackService{{
		FullName: "app-api",
		ServiceSpec: dockerswarm.ServiceSpec{TaskTemplate: dockerswarm.TaskSpec{
			ContainerSpec: &dockerswarm.ContainerSpec{Configs: []*dockerswarm.ConfigReference{{ConfigID: "old-used"}}},
		}},
	}}
	configs.EXPECT().Remove(gomock.Any(), "old-unused").Return(nil)

	cleaner := newRotatedResourceCleaner(secrets, configs, config.SecretRotationCleanupSpec{
		KeepLast: 1,
		MinAge:   specw.Duration{Value: time.Hour},
	})
	cleaner.now = func() time.Time { return now }

	result := cleaner.clean(
		context.Background(),
		"app",
		map[string]string{"settings": "app-settings-new"},
		nil,
		services,
		liveConfigs,
		nil,
	)

	assert.Equal(t, rotatedCleanupResult{Removed: 1}, result, "unexpected config cleanup result")
}

func TestRotatedResourceCleanerFailsClosedForIncompleteManagedLabels(t *testing.T) {
	ctrl := gomock.NewController(t)
	cleaner := newRotatedResourceCleaner(
		swarm.NewMockSecretManager(ctrl),
		swarm.NewMockConfigManager(ctrl),
		config.SecretRotationCleanupSpec{KeepLast: 1},
	)
	cleaner.now = time.Now

	resources := []swarm.Secret{
		managedSecret("old", "app-token-old", "token", time.Now().Add(-2*time.Hour)),
		{
			ID: "broken", Name: "app-token-broken", CreatedAt: time.Now().Add(-time.Hour),
			Labels: map[string]string{
				labelsdict.StackNamespace:                 "app",
				labelsdict.RotatedResourceManagedLabelKey: labelsdict.RotatedResourceManagedLabelValue,
			},
		},
	}

	result := cleaner.clean(context.Background(), "app", nil, nil, nil, nil, resources)

	require.Equal(t, rotatedCleanupResult{Skipped: 1}, result, "cleanup must fail closed")
}

func managedSecret(id, name, logicalName string, createdAt time.Time) swarm.Secret {
	return swarm.Secret{
		ID:        id,
		Name:      name,
		CreatedAt: createdAt,
		Labels: map[string]string{
			labelsdict.StackNamespace:                     "app",
			labelsdict.RotatedResourceManagedLabelKey:     labelsdict.RotatedResourceManagedLabelValue,
			labelsdict.RotatedResourceLogicalNameLabelKey: logicalName,
		},
	}
}
