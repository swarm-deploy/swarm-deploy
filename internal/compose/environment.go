package compose

import (
	"fmt"
	"strings"

	"github.com/go-faster/errors"
	"gopkg.in/yaml.v3"
)

const envPairParts = 2

type Environment struct {
	Map  map[string]string
	Keys []string

	isMap bool
}

func NewEnvironment(values []string) (*Environment, error) {
	env := &Environment{
		Map:  make(map[string]string, len(values)),
		Keys: make([]string, 0, len(values)),
	}

	for i, raw := range values {
		key, value, err := env.parseVar(raw)
		if err != nil {
			return nil, fmt.Errorf("environment[%d] %q: %w", i, raw, err)
		}

		env.Map[key] = value
		env.Keys = append(env.Keys, key)
	}

	return env, nil
}

func (e *Environment) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.MappingNode && node.Kind != yaml.SequenceNode {
		return errors.New("environment must be a map or a sequence node")
	}

	if node.Kind == yaml.MappingNode {
		e.Map = make(map[string]string, len(node.Content)/envPairParts)
		e.Keys = make([]string, 0, len(node.Content)/envPairParts)

		key := ""
		for i, child := range node.Content {
			if i%2 == 0 {
				key = child.Value
			} else {
				e.Map[key] = child.Value
				e.Keys = append(e.Keys, key)
			}
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

func (e *Environment) IsEmpty() bool {
	return len(e.Map) == 0
}

func (e *Environment) Clone() *Environment {
	values := make(map[string]string, len(e.Map))
	for k, v := range e.Map {
		values[k] = v
	}

	return &Environment{
		Map:   values,
		isMap: e.isMap,
	}
}

func (e *Environment) Has(key string) bool {
	_, has := e.Map[key]
	return has
}

func (e *Environment) Get(key string) (string, bool) {
	value, has := e.Map[key]
	return value, has
}

func (e *Environment) unmarshalFromSequence(node *yaml.Node) error {
	mmap := map[string]string{}
	keys := make([]string, 0, len(node.Content))

	for i, item := range node.Content {
		if item.Kind != yaml.ScalarNode {
			return fmt.Errorf("environment[%d] contains non-scalar node type", i)
		}

		key, value, err := e.parseVar(item.Value)
		if err != nil {
			return fmt.Errorf("environment[%d] %q: %w", i, item.Value, err)
		}

		mmap[key] = value
		keys = append(keys, key)
	}

	e.Map = mmap
	e.Keys = keys

	return nil
}

func (e *Environment) parseVar(raw string) (string, string, error) {
	chunks := strings.SplitN(raw, "=", envPairParts)
	if len(chunks) == envPairParts {
		return chunks[0], chunks[1], nil
	}

	return "", "", errors.New("contains non-pair value")
}
