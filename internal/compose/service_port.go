package compose

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/docker/go-connections/nat"

	dockerswarm "github.com/docker/docker/api/types/swarm"
	"go.yaml.in/yaml/v3"
)

type ServicePorts struct {
	Ports []ServicePort `json:"Ports"`

	isMap bool
}

type ServicePort struct {
	Published   int                               `yaml:"published" json:"published"`
	Target      int                               `yaml:"target" json:"target"`
	Protocol    dockerswarm.PortConfigProtocol    `yaml:"protocol,omitempty" json:"protocol,omitempty"`
	AppProtocol string                            `yaml:"app_protocol,omitempty" json:"app_protocol,omitempty"`
	Mode        dockerswarm.PortConfigPublishMode `yaml:"mode,omitempty" json:"mode,omitempty"`
	HostIP      string                            `yaml:"host_ip,omitempty" json:"host_ip,omitempty"`

	Extra map[string]interface{} `yaml:",inline" json:"Extra"`
}

func (sp *ServicePorts) UnmarshalYAML(root *yaml.Node) error { //nolint:gocognit // not need
	if root.Kind == yaml.MappingNode {
		published := 0

		for i, node := range root.Content {
			if i%2 == 0 {
				var err error
				published, err = strconv.Atoi(node.Value)
				if err != nil {
					return fmt.Errorf("parse value %q as published port: %w", node.Value, err)
				}
				continue
			}

			targetPort, err := strconv.Atoi(node.Value)
			if err != nil {
				return fmt.Errorf("parse value %q as target port: %w", node.Value, err)
			}

			sp.Ports = append(sp.Ports, defaultServicePort(published, targetPort))
		}

		sp.isMap = true
		return nil
	}

	if root.Kind != yaml.SequenceNode {
		return fmt.Errorf("expect sequence/mapping node, got %s", root.Tag)
	}

	for i, node := range root.Content {
		switch node.Kind { //nolint:exhaustive // expect only mapping or scalar
		case yaml.MappingNode:
			port := defaultServicePort(0, 0)
			if err := node.Decode(&port); err != nil {
				return fmt.Errorf("decode %d port: %w", i, err)
			}

			sp.Ports = append(sp.Ports, port)
		case yaml.ScalarNode:
			if strings.Contains(node.Value, "-") {
				ports, err := parsePortRange(node.Value)
				if err != nil {
					return fmt.Errorf("parse port range: %w", err)
				}
				sp.Ports = append(sp.Ports, ports...)
				continue
			}
			port, err := sp.parseStringView(node.Value)
			if err != nil {
				return fmt.Errorf("parse %q: %w", node.Value, err)
			}

			sp.Ports = append(sp.Ports, *port)
		default:
			return fmt.Errorf("expect mapping or string node, got %s", root.Tag)
		}
	}

	return nil
}

func (sp ServicePorts) MarshalYAML() (interface{}, error) {
	if sp.isMap {
		const mappingNodesMul = 2

		root := yaml.Node{
			Kind:    yaml.MappingNode,
			Content: make([]*yaml.Node, 0, mappingNodesMul*len(sp.Ports)),
		}

		for _, port := range sp.Ports {
			root.Content = append(root.Content, &yaml.Node{
				Kind:  yaml.ScalarNode,
				Value: strconv.Itoa(port.Published),
			})
			root.Content = append(root.Content, &yaml.Node{
				Kind:  yaml.ScalarNode,
				Value: strconv.Itoa(port.Target),
			})
		}

		return &root, nil
	}

	return sp.Ports, nil
}

func (sp *ServicePorts) parseStringView(s string) (*ServicePort, error) {
	port := defaultServicePort(0, 0)

	intVal := 0

	for i, char := range s {
		switch char {
		case ':':
			port.Published = intVal
			intVal = 0
		case '/':
			port.Target = intVal
			port.Protocol = dockerswarm.PortConfigProtocol(s[i+1:])
			return &port, nil
		default:
			if char < '0' || char > '9' {
				return nil, fmt.Errorf("invalid target character %q", char)
			}

			if intVal == 0 {
				intVal = int(char - '0')
			} else {
				intVal = intVal*10 + int(char-'0') //nolint:mnd // not need
			}

			if i == len(s)-1 {
				port.Target = intVal
			}
		}
	}

	if port.Protocol == "" {
		port.Protocol = dockerswarm.PortConfigProtocolTCP
	}

	return &port, nil
}

func defaultServicePort(published, target int) ServicePort {
	return ServicePort{
		Published: published,
		Target:    target,
		Protocol:  dockerswarm.PortConfigProtocolTCP,
		Mode:      dockerswarm.PortConfigPublishModeIngress,
	}
}

func parsePortRange(value string) ([]ServicePort, error) {
	mappings, err := nat.ParsePortSpec(value)
	if err != nil {
		return nil, err
	}
	result := make([]ServicePort, 0, len(mappings))
	for _, mapping := range mappings {
		published := 0
		if mapping.Binding.HostPort != "" {
			published, err = strconv.Atoi(mapping.Binding.HostPort)
			if err != nil {
				return nil, err
			}
		}
		port := defaultServicePort(published, mapping.Port.Int())
		port.Protocol = dockerswarm.PortConfigProtocol(mapping.Port.Proto())
		port.HostIP = mapping.Binding.HostIP
		result = append(result, port)
	}
	return result, nil
}
