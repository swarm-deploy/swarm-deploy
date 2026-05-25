package compose

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

type Command struct {
	Args []string

	isList bool
}

func (c *Command) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.ScalarNode {
		c.Args = []string{node.Value}
		return nil
	}

	if node.Kind == yaml.SequenceNode {
		c.isList = true

		return node.Decode(&c.Args)
	}

	return fmt.Errorf("expected string or sequence node, got %T", node.Kind)
}

func (c Command) MarshalYAML() (interface{}, error) {
	if len(c.Args) == 0 {
		return "", nil
	}

	if c.isList {
		return c.Args, nil
	}

	return c.Args[0], nil
}
