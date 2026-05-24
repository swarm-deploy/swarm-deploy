package compose

import (
	"fmt"
	"strconv"

	"gopkg.in/yaml.v3"
)

type ServicePorts struct {
	Ports []ServicePort

	isMap bool
}

type ServicePort struct {
	Published   string       `yaml:"published" json:"published"`
	Target      int          `yaml:"target" json:"target"`
	Protocol    PortProtocol `yaml:"protocol,omitempty" json:"protocol,omitempty"`
	AppProtocol string       `yaml:"app_protocol,omitempty" json:"app_protocol,omitempty"`
	Mode        string       `yaml:"mode,omitempty" json:"mode,omitempty"`
	HostIP      string       `yaml:"host_ip,omitempty" json:"host_ip,omitempty"`
}

type PortProtocol string

const (
	PortProtocolTCP PortProtocol = "tcp"
	PortProtocolUDP PortProtocol = "udp"
)

func (p PortProtocol) Valid() bool {
	return p == PortProtocolTCP || p == PortProtocolUDP
}

func (sp *ServicePorts) UnmarshalYAML(root *yaml.Node) error {
	if root.Kind == yaml.MappingNode {
		published := ""

		for i, node := range root.Content {
			if i%2 == 0 {
				published = node.Value
				continue
			}

			targetPort, err := strconv.Atoi(node.Value)
			if err != nil {
				return fmt.Errorf("parse value %q as port: %w", node.Value, err)
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
			port := defaultServicePort("", 0)
			if err := node.Decode(&port); err != nil {
				return fmt.Errorf("decode %d port: %w", i, err)
			}

			sp.Ports = append(sp.Ports, port)
		case yaml.ScalarNode:
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
				Value: port.Published,
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

func (sp *ServicePorts) parseStringView(s string) (*ServicePort, error) { //nolint:gocognit // not need
	if len(s) == 0 {
		return nil, fmt.Errorf("empty string")
	}

	published := ""
	protocol := ""
	colonIdx := -1
	slashIdx := -1
	targetVal := 0
	hasTarget := false
	inTarget := false

	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case ':':
			if colonIdx != -1 || i == 0 {
				return nil, fmt.Errorf("invalid colon")
			}
			colonIdx = i
			published = s[:i]
			inTarget = true
		case '/':
			if colonIdx == -1 {
				return nil, fmt.Errorf("slash before colon")
			}
			if slashIdx != -1 {
				return nil, fmt.Errorf("multiple slashes")
			}
			slashIdx = i
			inTarget = false
			protocol = s[i+1:]
		default:
			if inTarget {
				if c < '0' || c > '9' {
					return nil, fmt.Errorf("invalid target character")
				}
				targetVal = targetVal*10 + int(c-'0') //nolint:mnd // not need
				hasTarget = true
			}
		}
	}

	if colonIdx == -1 {
		return nil, fmt.Errorf("missing colon")
	}
	if !hasTarget {
		return nil, fmt.Errorf("empty target")
	}

	port := defaultServicePort(published, targetVal)
	if protocol != "" {
		port.Protocol = PortProtocol(protocol)
	}

	return &port, nil
}

func defaultServicePort(published string, target int) ServicePort {
	return ServicePort{
		Published: published,
		Target:    target,
		Protocol:  PortProtocolTCP,
	}
}
