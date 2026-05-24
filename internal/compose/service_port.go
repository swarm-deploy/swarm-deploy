package compose

import (
	"gopkg.in/yaml.v3"
)

type ServicePorts struct {
	Ports []ServicePort

	isMap bool
}

type ServicePort struct {
	Published string       `yaml:"published" json:"published"`
	Target    string       `yaml:"target" json:"target"`
	Protocol  PortProtocol `yaml:"protocol" json:"protocol"`
	Mode      string       `yaml:"mode" json:"mode"`
	HostIP    string       `yaml:"host_ip" json:"host_ip"`
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

			sp.Ports = append(sp.Ports, defaultServicePort(published, node.Value))
		}

		sp.isMap = true
		return nil
	}

	return nil
}

func (sp ServicePorts) MarshalYAML() (interface{}, error) {
	if sp.isMap {
		root := yaml.Node{
			Kind:    yaml.MappingNode,
			Content: make([]*yaml.Node, 0, 2*len(sp.Ports)),
		}

		for _, port := range sp.Ports {
			root.Content = append(root.Content, &yaml.Node{
				Kind:  yaml.ScalarNode,
				Value: port.Published,
			})
			root.Content = append(root.Content, &yaml.Node{
				Kind:  yaml.ScalarNode,
				Value: port.Target,
			})
		}

		return &root, nil
	}

	return nil, nil
}

func defaultServicePort(published string, target string) ServicePort {
	return ServicePort{
		Published: published,
		Target:    target,
		Protocol:  PortProtocolTCP,
	}
}
