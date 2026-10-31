package converter

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/docker/docker/api/types/container"
	dockerswarm "github.com/docker/docker/api/types/swarm"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer/resources"
)

func containerOptions(target *dockerswarm.ContainerSpec, service compose.Service, resolved resources.Resolved) error {
	target.User, target.Dir, target.Hostname = service.User, service.WorkingDir, service.Hostname
	target.ReadOnly, target.TTY, target.OpenStdin = service.ReadOnly, service.TTY, service.StdinOpen
	target.Init, target.StopSignal = clonePointer(service.Init), service.StopSignal
	target.OomScoreAdj = service.OomScoreAdj
	if target.OomScoreAdj < -1000 || target.OomScoreAdj > 1000 {
		return fmt.Errorf("oom_score_adj: must be between -1000 and 1000")
	}
	target.Isolation = container.Isolation(service.Isolation)
	if !slices.Contains([]string{"", "default", "process", "hyperv"}, service.Isolation) {
		return fmt.Errorf("isolation: expected default, process or hyperv")
	}
	target.Sysctls = maps.Clone(service.Sysctls)
	if service.DNS != nil || service.DNSSearch != nil {
		target.DNSConfig = &dockerswarm.DNSConfig{
			Nameservers: slices.Clone(service.DNS), Search: slices.Clone(service.DNSSearch),
		}
	}
	for _, entry := range service.ExtraHosts {
		host, ip, exists := strings.Cut(entry, "=")
		if !exists {
			host, ip, exists = strings.Cut(entry, ":")
		}
		if !exists || host == "" || ip == "" {
			return fmt.Errorf("extra_hosts: expected host:ip or host=ip")
		}
		target.Hosts = append(target.Hosts, ip+" "+host)
	}
	for _, name := range sortedKeys(service.Ulimits) {
		limit := service.Ulimits[name]
		if err := unsupported("ulimits."+name, limit.Extra); err != nil {
			return err
		}
		if limit.Soft < -1 || limit.Hard < -1 || (limit.Hard != -1 && (limit.Soft == -1 || limit.Soft > limit.Hard)) {
			return fmt.Errorf("ulimits.%s: invalid soft/hard limits", name)
		}
		target.Ulimits = append(target.Ulimits, &container.Ulimit{Name: name, Soft: limit.Soft, Hard: limit.Hard})
	}
	return credentialSpec(target, service.CredentialSpec, resolved)
}

func credentialSpec(
	target *dockerswarm.ContainerSpec, source *compose.CredentialSpec, resolved resources.Resolved,
) error {
	if source == nil {
		return nil
	}
	if err := unsupported("credential_spec", source.Extra); err != nil {
		return err
	}
	count := 0
	for _, value := range []string{source.Config, source.File, source.Registry} {
		if value != "" {
			count++
		}
	}
	if count != 1 {
		return fmt.Errorf("credential_spec: exactly one of config, file or registry is required")
	}
	credential := &dockerswarm.CredentialSpec{File: source.File, Registry: source.Registry}
	if source.Config != "" {
		config, exists := resolved.Configs[source.Config]
		if !exists {
			return fmt.Errorf("credential_spec.config: undefined config")
		}
		credential.Config = config.ID
		target.Configs = append(target.Configs, &dockerswarm.ConfigReference{
			ConfigID: config.ID, ConfigName: config.Name, Runtime: &dockerswarm.ConfigReferenceRuntimeTarget{},
		})
		slices.SortStableFunc(target.Configs, func(a, b *dockerswarm.ConfigReference) int {
			return compareStrings(a.ConfigName, b.ConfigName)
		})
	}
	target.Privileges = &dockerswarm.Privileges{CredentialSpec: credential}
	return nil
}
