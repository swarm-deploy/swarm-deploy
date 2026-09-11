package swarm

import (
	"github.com/docker/docker/client"
	"go.opentelemetry.io/otel/trace"

	"github.com/swarm-deploy/swarm-deploy/internal/shared/tracing"
)

type Swarm struct {
	// Services manages Docker swarm services.
	Services ServiceManager
	// Images manages Docker images.
	Images ImageManager
	// Secrets manages Docker swarm secrets.
	Secrets SecretManager
	// Configs manages Docker swarm configs.
	Configs *ConfigManager
	// Nodes manages Docker swarm nodes.
	Nodes NodeManager
	// Networks manages Docker networks.
	Networks NetworkManager
	// Plugins manages Docker plugins.
	Plugins *PluginManager
	// BinaryRunner executes docker CLI commands.
	BinaryRunner *BinaryRunner
}

func NewSwarm(dockerClient *client.Client, command string) *Swarm {
	swarm := newSwarm(dockerClient, command)

	tp, tracingEnabled := tracing.GetTracerProvider()
	if !tracingEnabled {
		return swarm
	}

	traceSwarm(tp, swarm)

	return swarm
}

func newSwarm(dockerClient *client.Client, command string) *Swarm {
	return &Swarm{
		Services:     newServiceManager(dockerClient),
		Images:       newImageManager(dockerClient),
		Secrets:      newSecretManager(dockerClient),
		Configs:      newConfigManager(dockerClient),
		Nodes:        newNodeManager(dockerClient),
		Networks:     newNetworkManager(dockerClient),
		Plugins:      newPluginManager(dockerClient),
		BinaryRunner: newBinaryRunner(command),
	}
}

func traceSwarm(tp trace.TracerProvider, swarm *Swarm) {
	tracer := tp.Tracer("github.com/swarm-deploy/internal/swarm")

	swarm.Services = traceServiceManager(tracer, swarm.Services)
}
