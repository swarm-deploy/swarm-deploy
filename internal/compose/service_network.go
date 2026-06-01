package compose

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

type ServiceNetworks struct {
	Names []string

	List     []*ServiceNetwork
	AliasMap map[string]*ServiceNetwork
	Aliases  []string

	onlyAlias bool
}

type ServiceNetwork struct {
	Alias string `yaml:"-"`

	// Resolved full name
	ResolvedName string `yaml:"-"`

	IPV4Address string   `yaml:"ipv4_address,omitempty" json:"ipv4_address,omitempty"`
	Aliases     []string `yaml:"aliases,omitempty" json:"aliases,omitempty"`
	// DriverOpts contains network attachment driver options.
	DriverOpts map[string]string `yaml:"driver_opts,omitempty" json:"driver_opts,omitempty"`

	Extra map[string]interface{} `yaml:",inline"`
}

func NewServiceNetworks(nets ...*ServiceNetwork) *ServiceNetworks {
	sn := &ServiceNetworks{}

	sn.Names = make([]string, 0, len(nets))
	sn.List = make([]*ServiceNetwork, 0, len(nets))
	sn.AliasMap = make(map[string]*ServiceNetwork, len(nets))
	sn.Aliases = make([]string, 0, len(nets))

	for _, net := range nets {
		sn.Names = append(sn.Names, net.ResolvedName)
		sn.List = append(sn.List, net)
		sn.AliasMap[net.Alias] = net
		sn.Aliases = append(sn.Aliases, net.Alias)
	}

	return sn
}

func (s *ServiceNetworks) GetNames() []string {
	if s == nil {
		return nil
	}
	return s.Names
}

func (s *ServiceNetworks) GetAliases() []string {
	if s == nil {
		return nil
	}
	return s.Aliases
}

func (s *ServiceNetworks) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.SequenceNode {
		s.AliasMap = make(map[string]*ServiceNetwork, len(node.Content))
		s.Aliases = make([]string, 0, len(node.Content))

		for i, child := range node.Content {
			if child.Kind != yaml.ScalarNode {
				return fmt.Errorf("network[%d].name expected as string, got %q", i, child.Tag)
			}

			network := &ServiceNetwork{
				Alias: child.Value,
			}

			s.List = append(s.List, network)
			s.AliasMap[child.Value] = network
			s.Aliases = append(s.Aliases, network.Alias)
			s.onlyAlias = true
		}
		return nil
	}

	if node.Kind != yaml.MappingNode {
		return fmt.Errorf("expected sequence or mapping node, got %q", node.Tag)
	}

	const networksLenDiv = 2

	s.AliasMap = make(map[string]*ServiceNetwork, len(node.Content)/networksLenDiv)
	s.Aliases = make([]string, 0, len(node.Content)/networksLenDiv)

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

		s.List = append(s.List, &network)
		s.AliasMap[alias] = &network
		s.Aliases = append(s.Aliases, alias)
	}

	return nil
}

func (s ServiceNetworks) MarshalYAML() (interface{}, error) {
	if s.onlyAlias {
		return s.Aliases, nil
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

func (s *ServiceNetworks) HasAlias(alias string) bool {
	if s == nil {
		return false
	}

	_, has := s.AliasMap[alias]
	return has
}
