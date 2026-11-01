package compose

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/go-faster/errors"
	"go.yaml.in/yaml/v3"
)

const envPairParts = 2

type Environment struct {
	// Unset records bare/null keys without an effective value. It survives snapshots
	// and rendered YAML so deployment never consults the controller environment.
	Unset []string          `json:"Unset,omitempty"`
	Map   map[string]string `json:"Map"`
	Keys  []string          `json:"Keys"`

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

		env.Unset = slices.DeleteFunc(env.Unset, func(existing string) bool { return existing == key })
		if !strings.Contains(raw, "=") {
			env.Unset = append(env.Unset, key)
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

	e.Unset = nil
	if node.Kind == yaml.MappingNode {
		return e.unmarshalFromMapping(node)
	}

	return e.unmarshalFromSequence(node)
}

func (e Environment) MarshalYAML() (interface{}, error) {
	if e.isMap {
		values := make(map[string]any, len(e.Map))
		for key, value := range e.Map {
			if slices.Contains(e.Unset, key) {
				values[key] = nil
			} else {
				values[key] = value
			}
		}
		return values, nil
	}

	values := make([]string, len(e.Map))
	i := 0

	for k, v := range e.Map {
		values[i] = k + "=" + v
		if slices.Contains(e.Unset, k) {
			values[i] = k
		}
		i++
	}

	sort.Strings(values)
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
		Keys:  slices.Clone(e.Keys),
		Unset: slices.Clone(e.Unset),
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

		e.Unset = slices.DeleteFunc(e.Unset, func(existing string) bool { return existing == key })
		if !strings.Contains(item.Value, "=") {
			e.Unset = append(e.Unset, key)
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

	if raw == "" {
		return "", "", errors.New("contains empty variable name")
	}
	return raw, "", nil
}

// resolve captures effective bare/null values during source preparation only.
func (e *Environment) resolve(lookup func(string) (string, bool)) {
	unresolved := make([]string, 0, len(e.Unset))
	for _, key := range e.Unset {
		if value, exists := lookup(key); exists {
			e.Map[key] = value
		} else {
			unresolved = append(unresolved, key)
		}
	}
	e.Unset = unresolved
}

func (e *Environment) unmarshalFromMapping(node *yaml.Node) error {
	e.Map = make(map[string]string, len(node.Content)/envPairParts)
	e.Keys = make([]string, 0, len(node.Content)/envPairParts)

	key := ""
	for i, child := range node.Content {
		if i%2 == 0 {
			key = child.Value
		} else {
			if child.Tag == "!!null" {
				e.Unset = append(e.Unset, key)
				e.Map[key] = ""
			} else {
				e.Map[key] = child.Value
			}
			e.Keys = append(e.Keys, key)
		}
	}

	e.isMap = true

	return nil
}
