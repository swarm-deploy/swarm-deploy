package compose

import (
	"fmt"
	"strings"

	"github.com/go-faster/errors"
	"gopkg.in/yaml.v3"
)

type Environment map[string]string

func (e *Environment) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.MappingNode && node.Kind != yaml.SequenceNode {
		return errors.New("environment must be a map or a sequence node")
	}

	if node.Kind == yaml.MappingNode {
		mmap := make(map[string]string)
		if err := node.Decode(&mmap); err != nil {
			return err
		}

		*e = mmap

		return nil
	}

	return e.unmarshalFromSequence(node)
}

func (e *Environment) unmarshalFromSequence(node *yaml.Node) error {
	mmap := map[string]string{}

	for i, item := range node.Content {
		if item.Kind != yaml.ScalarNode {
			return fmt.Errorf("environment[%d] contains non-scalar node type", i)
		}

		chunks := strings.SplitN(item.Value, "=", envPairParts)
		if len(chunks) == envPairParts {
			mmap[chunks[0]] = chunks[1]
		} else {
			return fmt.Errorf("environment[%d] contains non-pair value", i)
		}
	}

	*e = mmap

	return nil
}
