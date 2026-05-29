package swarm

import "github.com/docker/docker/client"

type Swarm struct {
	// Services manages Docker swarm services.
	Services ServiceManager
	// Secrets manages Docker swarm secrets.
	Secrets SecretManager
	// Configs manages Docker swarm configs.
	Configs *ConfigManager
	// Nodes manages Docker swarm nodes.
	Nodes *NodeManager
	// Networks manages Docker networks.
	Networks NetworkManager
	// Plugins manages Docker plugins.
	Plugins *PluginManager
	// BinaryRunner executes docker CLI commands.
	BinaryRunner *BinaryRunner
}

func NewSwarm(dockerClient *client.Client, command string) *Swarm {
	return &Swarm{
		Services:     newServiceManager(dockerClient),
		Secrets:      newSecretManager(dockerClient),
		Configs:      newConfigManager(dockerClient),
		Nodes:        newNodeManager(dockerClient),
		Networks:     newNetworkManager(dockerClient),
		Plugins:      newPluginManager(dockerClient),
		BinaryRunner: newBinaryRunner(command),
	}
}
