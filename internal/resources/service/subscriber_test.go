package service

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/resources/service/metadata"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	"github.com/swarm-deploy/webroute"
	"go.uber.org/mock/gomock"
)

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
			store, err := NewStore(filepath.Join(t.TempDir(), "services.json"))
			require.NoError(t, err)

			sub := NewSubscriber(store, inspector, images, &fakeSubscriberConfigReader{}, metadata.NewExtractor())
			serviceRef := swarm.NewServiceReference("payments", "api")
			testCase.setupMocks(inspector, images, serviceRef)

			err = sub.Handle(context.Background(), &events.DeploySuccess{
				StackName: "payments",
				Services: []compose.Service{
					{
						Name:  "api",
						Image: "ghcr.io/swarm-deploy/payments-api:v1.2.3",
					},
				},
			})
			require.NoError(t, err)

			info, ok := store.Get("payments", "api")
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

	ctrl := gomock.NewController(t)
	inspector := swarm.NewMockServiceManager(ctrl)
	images := swarm.NewMockImageManager(ctrl)
	configs := &fakeSubscriberConfigReader{
		configs: map[string]swarm.Config{
			"prod_pomerium_config": {
				Data: []byte(`
routes:
  - from: https://api.example.com
    to: http://api:8080
`),
			},
		},
	}
	store, err := NewStore(filepath.Join(t.TempDir(), "services.json"))
	require.NoError(t, err)

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

	sub := NewSubscriber(store, inspector, images, configs, metadata.NewExtractor())

	err = sub.Handle(context.Background(), &events.DeploySuccess{
		StackName: "prod",
		Services: []compose.Service{
			{
				Name:  "pomerium",
				Image: "ghcr.io/swarm-deploy/pomerium:v1",
			},
		},
	})
	require.NoError(t, err)

	info, ok := store.Get("prod", "pomerium")
	require.True(t, ok)
	assert.Equal(t, []webroute.Route{
		{
			Provider: webroute.ProviderNamePomerium,
			From: webroute.Address{
				Domain:  "api.example.com",
				Address: "api.example.com",
			},
			To: &webroute.Address{
				Domain:  "api",
				Address: "api:8080",
				Port:    "8080",
			},
		},
	}, info.WebRoutes)
	assert.Equal(t, []string{"prod_pomerium_config"}, configs.calls)
}

type fakeSubscriberConfigReader struct {
	configs map[string]swarm.Config
	calls   []string
}

func (r *fakeSubscriberConfigReader) Get(_ context.Context, configName string) (swarm.Config, error) {
	r.calls = append(r.calls, configName)

	return r.configs[configName], nil
}
