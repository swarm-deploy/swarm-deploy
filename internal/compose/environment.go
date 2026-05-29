package compose

import (
	"fmt"
	"strings"

	"github.com/go-faster/errors"
	"gopkg.in/yaml.v3"
)

const envPairParts = 2

type Environment struct {
	Map map[string]string

	isMap bool
}

func (e *Environment) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.MappingNode && node.Kind != yaml.SequenceNode {
		return errors.New("environment must be a map or a sequence node")
	}

	if node.Kind == yaml.MappingNode {
		if err := node.Decode(&e.Map); err != nil {
			return err
		}

		e.isMap = true

		return nil
	}

	return e.unmarshalFromSequence(node)
}

func (e Environment) MarshalYAML() (interface{}, error) {
	if e.isMap {
		return e.Map, nil
	}

	values := make([]string, len(e.Map))
	i := 0

	for k, v := range e.Map {
		values[i] = k + "=" + v
		i++
	}

	return values, nil
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

	e.Map = mmap

	return nil
}
