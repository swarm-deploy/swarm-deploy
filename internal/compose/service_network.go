package compose

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

type ServiceNetworks struct {
	Names []string

	List []*ServiceNetwork
	Map  map[string]*ServiceNetwork

	onlyAlias bool
}

type ServiceNetwork struct {
	Alias string `yaml:"-"`

	// Resolved full name
	ResolvedName string `yaml:"-"`

	IPV4Address string   `yaml:"ipv4_address,omitempty" json:"ipv4_address,omitempty"`
	Aliases     []string `yaml:"aliases,omitempty" json:"aliases,omitempty"`

	Extra map[string]interface{} `yaml:",inline"`
}

func (s *ServiceNetworks) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.SequenceNode {
		s.Names = make([]string, 0, len(node.Content))
		s.Map = make(map[string]*ServiceNetwork, len(node.Content))

		for i, child := range node.Content {
			if child.Kind != yaml.ScalarNode {
				return fmt.Errorf("network[%d].name expected as string, got %q", i, child.Tag)
			}

			network := &ServiceNetwork{
				Alias: child.Value,
			}

			s.Names = append(s.Names, child.Value)
			s.List = append(s.List, network)
			s.Map[child.Value] = network
			s.onlyAlias = true
		}
		return nil
	}

	if node.Kind != yaml.MappingNode {
		return fmt.Errorf("expected sequence or mapping node, got %q", node.Tag)
	}

	const networksLenDiv = 2

	s.Map = make(map[string]*ServiceNetwork, len(node.Content)/networksLenDiv)

	alias := ""
	for i, child := range node.Content {
		if i%2 == 0 {
			if child.Kind != yaml.ScalarNode {
				return fmt.Errorf("network[%d] alias expected as string, got %q", i, child.Tag)
			}

			alias = child.Value
			continue
		}

		var network ServiceNetwork
		if err := child.Decode(&network); err != nil {
			return fmt.Errorf("decode network %q: %w", alias, err)
		}

		network.Alias = alias

		s.Names = append(s.Names, alias)
		s.List = append(s.List, &network)
		s.Map[alias] = &network
	}

	return nil
}

func (s ServiceNetworks) MarshalYAML() (interface{}, error) {
	if s.onlyAlias {
		return s.Names, nil
	}

	const childNodesMul = 2

	root := &yaml.Node{
		Kind:    yaml.MappingNode,
		Content: make([]*yaml.Node, 0, childNodesMul*len(s.List)),
	}

	for _, network := range s.List {
		root.Content = append(root.Content, &yaml.Node{
			Kind:  yaml.ScalarNode,
			Value: network.Alias,
		})

		valueNode := yaml.Node{
			Kind: yaml.MappingNode,
		}

		if err := valueNode.Encode(&network); err != nil {
			return nil, fmt.Errorf("encode network %q: %w", network.Alias, err)
		}

		root.Content = append(root.Content, &valueNode)
	}

	return root, nil
}
