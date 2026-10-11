package compose

import (
	"encoding/json"
	"fmt"

	"go.yaml.in/yaml/v3"
)

type Command struct {
	Args []string `json:"Args"`

	isList bool
}

type commandJSON struct {
	// Args contains the parsed command values.
	Args []string `json:"Args"`
	// IsList preserves exec/list form versus scalar shell form.
	IsList bool `json:"IsList"`
}

func NewCommand(args []string) Command {
	return Command{
		Args:   args,
		isList: true,
	}
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

	return fmt.Errorf("expected string or sequence node, got %s", node.Tag)
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

// MarshalJSON preserves the scalar/list distinction because Compose gives
// those forms different command execution semantics.
func (c Command) MarshalJSON() ([]byte, error) {
	return json.Marshal(commandJSON{Args: c.Args, IsList: c.isList})
}

// UnmarshalJSON restores the execution form in persisted desired snapshots.
func (c *Command) UnmarshalJSON(data []byte) error {
	var value commandJSON
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	c.Args = value.Args
	c.isList = value.IsList
	return nil
}
