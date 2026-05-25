package compose

import (
	"errors"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

const labelPairParts = 2

type Labels struct {
	Map map[string]string

	isMap bool
}

func (e *Labels) UnmarshalYAML(node *yaml.Node) error {
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

func (e Labels) MarshalYAML() (interface{}, error) {
	if e.isMap {
		return e.Map, nil
	}

	values := make([]string, len(e.Map))
	i := 0

	for k, v := range e.Map {
		label := k
		if v != "" {
			label += "=" + v
		}

		values[i] = label
		i++
	}

	return values, nil
}

func (e *Labels) unmarshalFromSequence(node *yaml.Node) error {
	mmap := map[string]string{}

	for i, item := range node.Content {
		if item.Kind != yaml.ScalarNode {
			return fmt.Errorf("environment[%d] contains non-scalar node type", i)
		}

		chunks := strings.SplitN(item.Value, "=", labelPairParts)
		if len(chunks) == labelPairParts {
			mmap[chunks[0]] = chunks[1]
		} else {
			mmap[chunks[0]] = ""
		}
	}

	e.Map = mmap

	return nil
}
