package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/dispatcher"
	eventbus "github.com/swarm-deploy/swarm-deploy/internal/modules/event/dispatcher"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/outbox"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/resources/service/enrichment/metadata"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/resources/service/model"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/resources/service/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/storage"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	webroute "github.com/swarm-deploy/webroute/api"
	"go.uber.org/mock/gomock"
)

func TestSubscriberRepeatedDeliveryIsIdempotent(t *testing.T) {
	ctrl := gomock.NewController(t)
	db, err := storage.Open(t.Context(), t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	store := modelstore.NewSQLStore(db)
	inspector := swarm.NewMockServiceManager(ctrl)
	images := swarm.NewMockImageManager(ctrl)
	publisher := dispatcher.NewMockDispatcher(ctrl)
	serviceRef := swarm.NewServiceReference("app", "api")
	firstStatus := swarm.ServiceStatus{Spec: swarm.ServiceSpec{Image: "actual:1"}}
	secondStatus := swarm.ServiceStatus{Spec: swarm.ServiceSpec{Image: "actual:2"}}
	gomock.InOrder(
		inspector.EXPECT().GetStatus(gomock.Any(), serviceRef).Return(firstStatus, nil),
		inspector.EXPECT().GetStatus(gomock.Any(), serviceRef).Return(secondStatus, nil),
	)
	gomock.InOrder(
		images.EXPECT().Get(gomock.Any(), "actual:1").Return(swarm.Image{}, swarm.ErrImageNotFound),
		images.EXPECT().Get(gomock.Any(), "actual:2").Return(swarm.Image{}, swarm.ErrImageNotFound),
	)
	publisher.EXPECT().Publish(gomock.Any(), gomock.AssignableToTypeOf(&events.ServiceCatalogUpdated{})).
		DoAndReturn(func(_ context.Context, event events.Event) error {
			updated := event.(*events.ServiceCatalogUpdated)
			assert.Equal(t, "app", updated.StackName)
			return nil
		}).Times(1)
	subscriber := NewSubscriber(store, &swarm.Swarm{
		Services: inspector,
		Images:   images,
		Configs:  swarm.NewMockConfigManager(ctrl),
	}, metadata.NewExtractor(), db, publisher)
	event := events.Envelope{ID: "deploy-success", Event: &events.DeploySuccess{
		DeployEvent: events.DeployEvent{
			StackName: "app",
			StackDefinition: compose.File{Compose: compose.Compose{Services: []compose.Service{{
				Name: "api", Image: "desired:1",
			}}}},
		},
	}}

	require.NoError(t, subscriber.Handle(t.Context(), event))
	// Simulate redelivery after Handle committed but before the external delivery was acknowledged.
	require.NoError(t, subscriber.Handle(t.Context(), event))
	stored, exists, err := store.Find(t.Context(), "app", "api")
	require.NoError(t, err)
	require.True(t, exists)
	assert.Equal(t, "actual:1", stored.Spec.Image, "duplicate delivery must not replace the catalog")
	var receipt string
	require.NoError(t, db.Get(t.Context(), &receipt,
		"SELECT source_event_id FROM service_catalog_receipts"))
	assert.Equal(t, event.ID, receipt)
}

func TestSubscriberProjectionAndChildEventAreAtomic(t *testing.T) {
	ctrl := gomock.NewController(t)
	db, err := storage.Open(t.Context(), t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	store := modelstore.NewSQLStore(db)
	publisher := dispatcher.NewMockDispatcher(ctrl)
	gomock.InOrder(
		publisher.EXPECT().Publish(gomock.Any(), gomock.AssignableToTypeOf(&events.ServiceCatalogUpdated{})).
			Return(assert.AnError),
		publisher.EXPECT().Publish(gomock.Any(), gomock.AssignableToTypeOf(&events.ServiceCatalogUpdated{})).
			Return(nil),
	)
	subscriber := &Subscriber{db: db, store: store, events: publisher}
	require.NoError(t, store.ReplaceStack(t.Context(), "app", []model.Info{{Name: "old", Stack: "app"}}))

	event := &events.DeploySuccess{DeployEvent: events.DeployEvent{StackName: "app"}}
	services := []model.Info{{Name: "new", Stack: "app", Image: "new:1"}}
	require.ErrorIs(t, subscriber.persistProjection(t.Context(), "deploy-success", event, services), assert.AnError)
	_, oldExists, err := store.Find(t.Context(), "app", "old")
	require.NoError(t, err)
	assert.True(t, oldExists, "catalog replacement must roll back with child publication")
	var receipts int
	require.NoError(t, db.Get(t.Context(), &receipts, "SELECT count(*) FROM service_catalog_receipts"))
	assert.Zero(t, receipts, "receipt must roll back with catalog replacement")

	require.NoError(t, subscriber.persistProjection(t.Context(), "deploy-success", event, services))
	stored, exists, err := store.Find(t.Context(), "app", "new")
	require.NoError(t, err)
	require.True(t, exists)
	assert.Equal(t, "new:1", stored.Image)
	require.NoError(t, db.Get(t.Context(), &receipts, "SELECT count(*) FROM service_catalog_receipts"))
	assert.Equal(t, 1, receipts)
}

func TestSubscriberStaleDeploymentDoesNotPersistReceiptOrCatalog(t *testing.T) {
	ctrl := gomock.NewController(t)
	db, err := storage.Open(t.Context(), t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	store := modelstore.NewSQLStore(db)
	require.NoError(t, store.ReplaceStack(t.Context(), "app", []model.Info{{Name: "current", Stack: "app"}}))
	now := time.Date(2026, 4, 5, 6, 7, 8, 9, time.UTC)
	_, err = db.Exec(t.Context(), `INSERT INTO deployments
		(id,stack,"commit",status,error_code,phase,started_at,finished_at,
		 desired_payload,details_payload,changes_payload)
		VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		"current-deployment", "app", "commit", "succeeded", "", "completed", now, now,
		`{}`, `{}`, `[]`)
	require.NoError(t, err)
	_, err = db.Exec(t.Context(),
		"INSERT INTO desired_snapshots(stack,deployment_id) VALUES (?,?)", "app", "current-deployment")
	require.NoError(t, err)
	subscriber := &Subscriber{db: db, store: store, events: dispatcher.NewMockDispatcher(ctrl)}
	stale := &events.DeploySuccess{DeployEvent: events.DeployEvent{
		DeploymentID: "stale-deployment", StackName: "app",
	}}

	require.NoError(t, subscriber.persistProjection(t.Context(), "stale-event", stale,
		[]model.Info{{Name: "stale", Stack: "app"}}))
	_, currentExists, err := store.Find(t.Context(), "app", "current")
	require.NoError(t, err)
	assert.True(t, currentExists)
	var receipts int
	require.NoError(t, db.Get(t.Context(), &receipts, "SELECT count(*) FROM service_catalog_receipts"))
	assert.Zero(t, receipts)
}

func TestSubscriberHandle(t *testing.T) {
	t.Parallel()

	type expectation struct {
		image         string
		environment   map[string]string
		spec          swarm.ServiceSpec
		repositoryURL string
		description   string
	}

	testCases := []struct {
		name       string
		setupMocks func(*swarm.MockServiceManager, *swarm.MockImageManager, swarm.ServiceReference)
		expected   expectation
	}{
		{
			name: "persists swarm status spec",
			setupMocks: func(
				inspector *swarm.MockServiceManager,
				images *swarm.MockImageManager,
				serviceRef swarm.ServiceReference,
			) {
				inspector.EXPECT().
					GetStatus(gomock.Any(), serviceRef).
					Return(swarm.ServiceStatus{
						Stack:   "payments",
						Service: "api",
						Spec: swarm.ServiceSpec{
							Image:             "ghcr.io/swarm-deploy/payments-api:v1.2.3",
							Mode:              "replicated",
							Replicas:          2,
							RequestedRAMBytes: 268435456,
							RequestedCPUNano:  500000000,
							LimitRAMBytes:     536870912,
							LimitCPUNano:      1000000000,
							Labels: map[string]string{
								"com.docker.stack.namespace": "payments",
							},
							Secrets: []swarm.ServiceSecret{
								{
									SecretName: "payments_db_password",
									Target:     "/run/secrets/payments_db_password",
								},
							},
							Network: []swarm.ServiceNetwork{
								{
									Target:  "payments_default",
									Aliases: []string{"api"},
								},
							},
						},
						ContainerLabels: map[string]string{
							"org.swarm-deploy.service.type": "monitoring",
						},
						ContainerEnv: []string{
							"UNRELATED=value",
						},
					}, nil)
				images.EXPECT().
					Get(gomock.Any(), "ghcr.io/swarm-deploy/payments-api:v1.2.3").
					Return(swarm.Image{
						Ref: "ghcr.io/swarm-deploy/payments-api:v1.2.3",
						Labels: map[string]string{
							"org.opencontainers.image.source":      "github.com/acme/payments-api",
							"org.opencontainers.image.description": "Payments API",
						},
					}, nil)
			},
			expected: expectation{
				image: "ghcr.io/swarm-deploy/payments-api:v1.2.3",
				environment: map[string]string{
					"UNRELATED": "value",
				},
				spec: swarm.ServiceSpec{
					Image:             "ghcr.io/swarm-deploy/payments-api:v1.2.3",
					Mode:              "replicated",
					Replicas:          2,
					RequestedRAMBytes: 268435456,
					RequestedCPUNano:  500000000,
					LimitRAMBytes:     536870912,
					LimitCPUNano:      1000000000,
					Labels: map[string]string{
						"com.docker.stack.namespace": "payments",
					},
					Secrets: []swarm.ServiceSecret{
						{
							SecretName: "payments_db_password",
							Target:     "/run/secrets/payments_db_password",
						},
					},
					Network: []swarm.ServiceNetwork{
						{
							Target:  "payments_default",
							Aliases: []string{"api"},
						},
					},
				},
				repositoryURL: "github.com/acme/payments-api",
				description:   "Payments API",
			},
		},
		{
			name: "falls back to minimal spec when status unavailable",
			setupMocks: func(
				inspector *swarm.MockServiceManager,
				images *swarm.MockImageManager,
				serviceRef swarm.ServiceReference,
			) {
				inspector.EXPECT().
					GetStatus(gomock.Any(), serviceRef).
					Return(swarm.ServiceStatus{}, assert.AnError)
				images.EXPECT().
					Get(gomock.Any(), "ghcr.io/swarm-deploy/payments-api:v1.2.3").
					Return(swarm.Image{
						Ref: "ghcr.io/swarm-deploy/payments-api:v1.2.3",
						Labels: map[string]string{
							"org.opencontainers.image.source": "github.com/acme/payments-api",
							"org.opencontainers.image.title":  "Payments API",
						},
					}, nil)
			},
			expected: expectation{
				image:       "ghcr.io/swarm-deploy/payments-api:v1.2.3",
				environment: nil,
				spec: swarm.ServiceSpec{
					Image: "ghcr.io/swarm-deploy/payments-api:v1.2.3",
				},
				repositoryURL: "github.com/acme/payments-api",
				description:   "Payments API",
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			ctrl := gomock.NewController(t)
			inspector := swarm.NewMockServiceManager(ctrl)
			images := swarm.NewMockImageManager(ctrl)
			configs := swarm.NewMockConfigManager(ctrl)
			db, err := storage.Open(context.Background(), t.TempDir())
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, db.Close()) })
			store := modelstore.NewSQLStore(db)
			require.NoError(t, err)

			sub := NewSubscriber(store, &swarm.Swarm{
				Services: inspector,
				Images:   images,
				Configs:  configs,
			}, metadata.NewExtractor(), db, eventbus.NewOutboxDispatcher(outbox.New(db)))
			serviceRef := swarm.NewServiceReference("payments", "api")
			testCase.setupMocks(inspector, images, serviceRef)

			err = sub.Handle(context.Background(), events.Envelope{ID: "deploy", Event: &events.DeploySuccess{
				DeployEvent: events.DeployEvent{
					StackName: "payments",
					StackDefinition: compose.File{
						Compose: compose.Compose{
							Services: []compose.Service{
								{
									Name:  "api",
									Image: "ghcr.io/swarm-deploy/payments-api:v1.2.3",
								},
							},
						},
					},
				},
			}})
			require.NoError(t, err)

			info, ok, readErr := store.Find(context.Background(), "payments", "api")
			require.NoError(t, readErr)
			require.True(t, ok)
			assert.Equal(t, testCase.expected.image, info.Image)
			assert.Equal(t, testCase.expected.environment, info.Environment)
			assert.Equal(t, testCase.expected.spec, info.Spec)
			assert.Equal(t, testCase.expected.repositoryURL, info.RepositoryURL)
			assert.Equal(t, testCase.expected.description, info.Description)
		})
	}
}

func TestSubscriberHandleLoadsWebRouteConfigs(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name            string
		desiredData     []byte
		desiredPresent  bool
		swarmConfigData []byte
		loadSwarmConfig bool
		expectedDomain  string
	}{
		{
			name: "loads config from swarm when desired config is unavailable",
			swarmConfigData: []byte("routes:\n" +
				"  - from: https://api.example.com\n" +
				"    to: http://api:8080\n"),
			loadSwarmConfig: true,
			expectedDomain:  "api.example.com",
		},
		{
			name:           "loads config from desired data before swarm",
			desiredPresent: true,
			desiredData: []byte("routes:\n" +
				"  - from: https://file.example.com\n" +
				"    to: http://api:8080\n"),
			expectedDomain: "file.example.com",
		},
		{
			name:           "falls back to swarm when desired data is absent",
			desiredPresent: true,
			swarmConfigData: []byte("routes:\n" +
				"  - from: https://fallback.example.com\n" +
				"    to: http://api:8080\n"),
			loadSwarmConfig: true,
			expectedDomain:  "fallback.example.com",
		},
		{
			name:            "uses empty desired data without falling back to swarm",
			desiredPresent:  true,
			desiredData:     []byte{},
			swarmConfigData: []byte("routes:\n  - from: https://fallback.example.com\n"),
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			ctrl := gomock.NewController(t)
			inspector := swarm.NewMockServiceManager(ctrl)
			images := swarm.NewMockImageManager(ctrl)
			configs := swarm.NewMockConfigManager(ctrl)
			db, err := storage.Open(context.Background(), t.TempDir())
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, db.Close()) })
			store := modelstore.NewSQLStore(db)
			require.NoError(t, err)

			desiredConfigRef := compose.ObjectRef{
				Source: "pomerium_config",
				Target: "/etc/pomerium/config.yaml",
			}
			desiredConfigs := compose.Configs(nil)
			if testCase.desiredPresent {
				desiredConfigs = compose.Configs{
					"pomerium_config": {Data: testCase.desiredData},
				}
			}

			serviceRef := swarm.NewServiceReference("prod", "pomerium")
			inspector.EXPECT().
				GetStatus(gomock.Any(), serviceRef).
				Return(swarm.ServiceStatus{
					Stack:   "prod",
					Service: "pomerium",
					Spec: swarm.ServiceSpec{
						Image: "ghcr.io/swarm-deploy/pomerium:v1",
						Configs: []swarm.ServiceConfig{
							{
								ConfigName: "prod_pomerium_config",
								Target:     "/etc/pomerium/config.yaml",
							},
						},
					},
					ContainerConfigs: []swarm.ServiceConfig{
						{
							ConfigName: "prod_pomerium_config",
							Target:     "/etc/pomerium/config.yaml",
						},
					},
				}, nil)
			images.EXPECT().
				Get(gomock.Any(), "ghcr.io/swarm-deploy/pomerium:v1").
				Return(swarm.Image{Ref: "ghcr.io/swarm-deploy/pomerium:v1"}, swarm.ErrImageNotFound)
			if testCase.loadSwarmConfig {
				configs.EXPECT().
					Get(gomock.Any(), "prod_pomerium_config").
					Return(swarm.Config{Data: testCase.swarmConfigData}, nil)
			}

			sub := NewSubscriber(store, &swarm.Swarm{
				Services: inspector,
				Images:   images,
				Configs:  configs,
			}, metadata.NewExtractor(), db, eventbus.NewOutboxDispatcher(outbox.New(db)))

			err = sub.Handle(context.Background(), events.Envelope{ID: "deploy", Event: &events.DeploySuccess{
				DeployEvent: events.DeployEvent{
					StackName: "prod",
					StackDefinition: compose.File{
						Compose: compose.Compose{
							Configs: desiredConfigs,
							Services: []compose.Service{
								{
									Name:    "pomerium",
									Image:   "ghcr.io/swarm-deploy/pomerium:v1",
									Configs: []compose.ObjectRef{desiredConfigRef},
								},
							},
						},
					},
				},
			}})
			require.NoError(t, err)

			info, ok, readErr := store.Find(context.Background(), "prod", "pomerium")
			require.NoError(t, readErr)
			require.True(t, ok)
			if testCase.expectedDomain == "" {
				assert.Empty(t, info.WebRoutes)
				return
			}
			assert.Equal(t, []webroute.WebRoute{
				{
					Provider: webroute.ProviderNamePomerium,
					From: webroute.Address{
						Domain:  testCase.expectedDomain,
						Address: testCase.expectedDomain,
					},
					To: &webroute.Address{
						Domain:  "api",
						Address: "api:8080",
						Port:    "8080",
					},
				},
			}, info.WebRoutes)
		})
	}
}
