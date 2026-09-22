package handlers

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	generated "github.com/swarm-deploy/swarm-deploy/internal/entrypoints/webserver/generated"
	gitx "github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/git"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	"go.uber.org/mock/gomock"
)

func TestHandlerGetStackManifestos(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	gitRepository := gitx.NewMockRepository(ctrl)
	serviceInspector := swarm.NewMockServiceManager(ctrl)
	networkManager := swarm.NewMockNetworkManager(ctrl)
	cfg := newConfigWithStacks([]config.StackSpec{
		{
			Name:        "payments",
			ComposeFile: "stacks/payments.yaml",
		},
	})

	desiredManifest := []byte("services:\n  worker:\n    image: ghcr.io/swarm-deploy/payments-worker:v1.2.3\n  api:\n    image: ghcr.io/swarm-deploy/payments-api:v1.2.3\n")
	gitRepository.EXPECT().
		ReadFile(gomock.Any(), "stacks/payments.yaml").
		Return(desiredManifest, nil)

	replicas := uint64(3)
	serviceInspector.EXPECT().
		ListStackServices(gomock.Any(), "payments").
		Return([]swarm.StackService{
			{
				Name:     "api",
				Image:    "ghcr.io/swarm-deploy/payments-api:v1.2.4",
				Mode:     "replicated",
				Replicas: &replicas,
			},
			{
				Name:  "worker",
				Image: "ghcr.io/swarm-deploy/payments-worker:v1.2.4",
			},
		}, nil)
	networkManager.EXPECT().
		Map(gomock.Any(), gomock.Any()).
		Return(map[string]swarm.Network{}, nil)

	h := &handler{
		stackProvider:    cfg,
		git:              gitRepository,
		serviceInspector: serviceInspector,
		networks:         networkManager,
		composeLoader:    composeLoaderForTest(desiredManifest),
	}

	resp, err := h.GetStackManifestos(context.Background(), generated.GetStackManifestosParams{
		Stack: "payments",
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, string(desiredManifest), resp.Desired)
	assert.Contains(t, resp.Live, "services:")
	assert.Contains(t, resp.Live, "api:")
	assert.Contains(t, resp.Live, "image: ghcr.io/swarm-deploy/payments-api:v1.2.4")
	assert.Contains(t, resp.Live, "mode: replicated")
	assert.Contains(t, resp.Live, "replicas: 3")
	require.Contains(t, resp.Live, "  worker:")
	assert.Less(t, strings.Index(resp.Live, "  worker:"), strings.Index(resp.Live, "  api:"))
}

func TestHandlerGetStackManifestos_StackNotFound(t *testing.T) {
	t.Parallel()

	h := &handler{
		stackProvider: newConfigWithStacks([]config.StackSpec{
			{
				Name:        "payments",
				ComposeFile: "stacks/payments.yaml",
			},
		}),
	}

	_, err := h.GetStackManifestos(context.Background(), generated.GetStackManifestosParams{
		Stack: "billing",
	})
	require.Error(t, err)

	var statusErr *statusError
	require.True(t, errors.As(err, &statusErr))
	assert.Equal(t, 404, statusErr.code)
	assert.Equal(t, "stack billing not found", statusErr.Error())
}

func TestHandlerGetStackManifestos_GitReadError(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	gitRepository := gitx.NewMockRepository(ctrl)
	cfg := newConfigWithStacks([]config.StackSpec{
		{
			Name:        "payments",
			ComposeFile: "stacks/payments.yaml",
		},
	})

	gitRepository.EXPECT().
		ReadFile(gomock.Any(), "stacks/payments.yaml").
		Return(nil, errors.New("read failed"))

	h := &handler{
		stackProvider: cfg,
		git:           gitRepository,
	}

	_, err := h.GetStackManifestos(context.Background(), generated.GetStackManifestosParams{
		Stack: "payments",
	})
	require.Error(t, err)

	var statusErr *statusError
	require.True(t, errors.As(err, &statusErr))
	assert.Equal(t, 500, statusErr.code)
	assert.Equal(t, "unable to get stack desired manifest", statusErr.Error())
}

func TestHandlerGetStackManifestos_DesiredComposeLoadError(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	gitRepository := gitx.NewMockRepository(ctrl)
	cfg := newConfigWithStacks([]config.StackSpec{
		{
			Name:        "payments",
			ComposeFile: "stacks/payments.yaml",
		},
	})
	invalidManifest := []byte("services: [")

	gitRepository.EXPECT().
		ReadFile(gomock.Any(), "stacks/payments.yaml").
		Return(invalidManifest, nil)

	h := &handler{
		stackProvider: cfg,
		git:           gitRepository,
		composeLoader: composeLoaderForTest(invalidManifest),
	}

	_, err := h.GetStackManifestos(context.Background(), generated.GetStackManifestosParams{
		Stack: "payments",
	})
	require.Error(t, err)

	var statusErr *statusError
	require.True(t, errors.As(err, &statusErr))
	assert.Equal(t, 500, statusErr.code)
	assert.Equal(t, "unable to get stack desired manifest", statusErr.Error())
}

func TestHandlerGetStackManifestos_ListStackServicesError(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	gitRepository := gitx.NewMockRepository(ctrl)
	serviceInspector := swarm.NewMockServiceManager(ctrl)
	cfg := newConfigWithStacks([]config.StackSpec{
		{
			Name:        "payments",
			ComposeFile: "stacks/payments.yaml",
		},
	})

	gitRepository.EXPECT().
		ReadFile(gomock.Any(), "stacks/payments.yaml").
		Return([]byte("services:\n  api:\n    image: ghcr.io/swarm-deploy/payments-api:v1.2.3\n"), nil)

	serviceInspector.EXPECT().
		ListStackServices(gomock.Any(), "payments").
		Return(nil, errors.New("swarm unavailable"))

	h := &handler{
		stackProvider:    cfg,
		git:              gitRepository,
		serviceInspector: serviceInspector,
		composeLoader: composeLoaderForTest(
			[]byte("services:\n  api:\n    image: ghcr.io/swarm-deploy/payments-api:v1.2.3\n"),
		),
	}

	_, err := h.GetStackManifestos(context.Background(), generated.GetStackManifestosParams{
		Stack: "payments",
	})
	require.Error(t, err)

	var statusErr *statusError
	require.True(t, errors.As(err, &statusErr))
	assert.Equal(t, 500, statusErr.code)
	assert.Equal(t, "unable to list stack services", statusErr.Error())
}

func TestHandlerGetStackManifestos_LiveManifestError(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	gitRepository := gitx.NewMockRepository(ctrl)
	serviceInspector := swarm.NewMockServiceManager(ctrl)
	networkManager := swarm.NewMockNetworkManager(ctrl)
	cfg := newConfigWithStacks([]config.StackSpec{
		{
			Name:        "payments",
			ComposeFile: "stacks/payments.yaml",
		},
	})

	gitRepository.EXPECT().
		ReadFile(gomock.Any(), "stacks/payments.yaml").
		Return([]byte("services:\n  api:\n    image: ghcr.io/swarm-deploy/payments-api:v1.2.3\n"), nil)

	serviceInspector.EXPECT().
		ListStackServices(gomock.Any(), "payments").
		Return([]swarm.StackService{
			{
				Name:  "api",
				Image: "ghcr.io/swarm-deploy/payments-api:v1.2.4",
			},
		}, nil)
	networkManager.EXPECT().
		Map(gomock.Any(), gomock.Any()).
		Return(nil, errors.New("swarm unavailable"))

	h := &handler{
		stackProvider:    cfg,
		git:              gitRepository,
		serviceInspector: serviceInspector,
		networks:         networkManager,
		composeLoader: composeLoaderForTest(
			[]byte("services:\n  api:\n    image: ghcr.io/swarm-deploy/payments-api:v1.2.3\n"),
		),
	}

	_, err := h.GetStackManifestos(context.Background(), generated.GetStackManifestosParams{
		Stack: "payments",
	})
	require.Error(t, err)

	var statusErr *statusError
	require.True(t, errors.As(err, &statusErr))
	assert.Equal(t, 500, statusErr.code)
	assert.Equal(t, "unable to get stack live manifest", statusErr.Error())
}

func composeLoaderForTest(manifest []byte) compose.FileLoader {
	return compose.NewFileLoaderWithReader(func(context.Context, string) ([]byte, error) {
		return manifest, nil
	})
}

func newConfigWithStacks(stacks []config.StackSpec) *config.Config {
	return &config.Config{
		Spec: config.Spec{
			Stacks: stacks,
		},
	}
}
