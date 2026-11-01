package compose

import (
	"fmt"
	"strings"

	"github.com/swarm-deploy/swarm-deploy/internal/shared/labelsdict"
	"go.yaml.in/yaml/v3"
)

type Services []Service

type Service struct {
	Name    string  `yaml:"-" json:"name"`
	Image   string  `yaml:"image" json:"image"`
	Command Command `yaml:"command,omitempty" json:"command,omitempty"`
	// Entrypoint overrides the image entrypoint.
	Entrypoint Command `yaml:"entrypoint,omitempty" json:"entrypoint,omitempty"`
	// StopGracePeriod is the time Docker gives a task to exit before forcefully stopping it.
	StopGracePeriod string `yaml:"stop_grace_period,omitempty" json:"stop_grace_period,omitempty"`
	// CapAdd lists Linux capabilities added to the service containers.
	CapAdd []string `yaml:"cap_add,omitempty" json:"cap_add,omitempty"`
	// CapDrop lists Linux capabilities removed from the service containers.
	CapDrop []string `yaml:"cap_drop,omitempty" json:"cap_drop,omitempty"`

	// User configures the Compose user option.
	User string `yaml:"user,omitempty" json:"user,omitempty"`
	// WorkingDir configures the Compose working_dir option.
	WorkingDir string `yaml:"working_dir,omitempty" json:"working_dir,omitempty"`
	// Hostname configures the Compose hostname option.
	Hostname string `yaml:"hostname,omitempty" json:"hostname,omitempty"`
	// DNS configures the Compose dns option.
	DNS StringList `yaml:"dns,omitempty" json:"dns,omitempty"`
	// DNSSearch configures the Compose dns_search option.
	DNSSearch StringList `yaml:"dns_search,omitempty" json:"dns_search,omitempty"`
	// ExtraHosts configures the Compose extra_hosts option.
	ExtraHosts HostsList `yaml:"extra_hosts,omitempty" json:"extra_hosts,omitempty"`
	// ReadOnly configures the Compose read_only option.
	ReadOnly bool `yaml:"read_only,omitempty" json:"read_only,omitempty"`
	// Init configures the Compose init option.
	Init *bool `yaml:"init,omitempty" json:"init,omitempty"`
	// TTY configures the Compose tty option.
	TTY bool `yaml:"tty,omitempty" json:"tty,omitempty"`
	// StdinOpen configures the Compose stdin_open option.
	StdinOpen bool `yaml:"stdin_open,omitempty" json:"stdin_open,omitempty"`
	// StopSignal configures the Compose stop_signal option.
	StopSignal string `yaml:"stop_signal,omitempty" json:"stop_signal,omitempty"`
	// Sysctls configures the Compose sysctls option.
	Sysctls StringMap `yaml:"sysctls,omitempty" json:"sysctls,omitempty"`
	// Ulimits configures the Compose ulimits option.
	Ulimits map[string]Ulimit `yaml:"ulimits,omitempty" json:"ulimits,omitempty"`
	// Tmpfs configures the Compose tmpfs option.
	Tmpfs StringList `yaml:"tmpfs,omitempty" json:"tmpfs,omitempty"`
	// Isolation configures the Compose isolation option.
	Isolation string `yaml:"isolation,omitempty" json:"isolation,omitempty"`
	// CredentialSpec configures the Compose credential_spec option.
	CredentialSpec *CredentialSpec `yaml:"credential_spec,omitempty" json:"credential_spec,omitempty"`
	// OomScoreAdj configures the Compose oom_score_adj option.
	OomScoreAdj int64 `yaml:"oom_score_adj,omitempty" json:"oom_score_adj,omitempty"`

	Healthcheck *ServiceHealth   `yaml:"healthcheck,omitempty" json:"healthcheck,omitempty"`
	Ports       ServicePorts     `yaml:"ports,omitempty" json:"ports,omitempty"`
	Networks    *ServiceNetworks `yaml:"networks,omitempty" json:"networks,omitempty"`
	Secrets     []ObjectRef      `yaml:"secrets,omitempty" json:"secrets,omitempty"`
	Configs     []ObjectRef      `yaml:"configs,omitempty" json:"configs,omitempty"`
	Labels      Labels           `yaml:"labels,omitempty" json:"labels,omitempty"`
	// EnvFiles contains environment files loaded for the service.
	EnvFiles    EnvFiles       `yaml:"env_file,omitempty" json:"env_file,omitempty"`
	Environment Environment    `yaml:"environment,omitempty" json:"environment,omitempty"`
	InitJobs    []InitJob      `yaml:"x-init-deploy-jobs,omitempty" json:"init_jobs,omitempty"`
	Deploy      ServiceDeploy  `yaml:"deploy,omitempty" json:"deploy"`
	Logging     ServiceLogging `yaml:"logging,omitempty" json:"logging,omitempty"`
	Volumes     ServiceVolumes `yaml:"volumes,omitempty" json:"volumes"`
	// IgnoredOptions contains tolerated Swarm-ignored source keys; never persisted.
	IgnoredOptions []string `yaml:"-" json:"-"`
}

type ServiceHealth struct {
	Test          Command `yaml:"test,omitempty" json:"test,omitempty"`
	Interval      string  `yaml:"interval,omitempty" json:"interval,omitempty"`
	Timeout       string  `yaml:"timeout,omitempty" json:"timeout,omitempty"`
	Retries       *uint64 `yaml:"retries,omitempty" json:"retries,omitempty"`
	StartPeriod   string  `yaml:"start_period,omitempty" json:"start_period,omitempty"`
	StartInterval string  `yaml:"start_interval,omitempty" json:"start_interval,omitempty"`
	Disable       bool    `yaml:"disable,omitempty" json:"disable,omitempty"`
}

type ServiceLogging struct {
	Driver  string            `yaml:"driver,omitempty" json:"driver,omitempty"`
	Options map[string]string `yaml:"options,omitempty" json:"options,omitempty"`
}

// SelfHealPolicy returns the service-level self-heal policy and whether it was specified.
func (s Service) SelfHealPolicy() (bool, bool, error) {
	return s.booleanPolicy(labelsdict.ServiceSyncPolicySelfHealLabelKey)
}

// ReplicaDriftPolicy returns the service-level replica drift policy and whether it was specified.
func (s Service) ReplicaDriftPolicy() (bool, bool, error) {
	return s.booleanPolicy(labelsdict.ServiceSyncPolicySelfHealReplicasLabelKey)
}

// ReplicasManagedPolicy returns replica ownership and whether it was specified.
func (s Service) ReplicasManagedPolicy() (bool, bool, error) {
	return s.booleanPolicy(labelsdict.ServiceSyncPolicyReplicasManagedLabelKey)
}

func (s Service) booleanPolicy(label string) (bool, bool, error) {
	raw, exists := s.Deploy.Labels.Map[label]
	if !exists {
		return false, false, nil
	}

	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "true", "1", "yes", "y", "on":
		return true, true, nil
	case "false", "0", "no", "n", "off":
		return false, true, nil
	default:
		return false, true, fmt.Errorf("must be boolean, got %q", raw)
	}
}

func (s *Services) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind != yaml.MappingNode {
		return fmt.Errorf("expected mapping node, got %T", n.Kind)
	}

	name := ""
	services := make([]Service, 0)

	for i, cn := range n.Content {
		if i%2 == 0 {
			name = cn.Value
			continue
		}

		if strings.HasPrefix(name, "x-") {
			continue
		}

		var srv Service

		err := cn.Decode(&srv)
		if err != nil {
			return fmt.Errorf("decode service with name %q: %w", name, err)
		}

		srv.Name = name

		services = append(services, srv)
	}

	*s = services

	return nil
}

func (s Services) MarshalYAML() (interface{}, error) {
	const nodesMul = 2

	root := yaml.Node{
		Kind:    yaml.MappingNode,
		Content: make([]*yaml.Node, 0, nodesMul*len(s)),
	}

	for _, service := range s {
		root.Content = append(root.Content, &yaml.Node{
			Kind:  yaml.ScalarNode,
			Value: service.Name,
		})

		node := &yaml.Node{
			Kind: yaml.MappingNode,
		}

		err := node.Encode(service)
		if err != nil {
			return nil, fmt.Errorf("encode service with name %q: %w", service.Name, err)
		}

		root.Content = append(root.Content, node)
	}

	return &root, nil
}
