package selfheal

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/controller/stackloop/policies"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/labelsdict"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/yamledit"
	"go.yaml.in/yaml/v3"
)

const label = labelsdict.ServiceSyncPolicySelfHealLabelKey

// State describes the effective self-heal setting in the proposal base.
type State struct {
	// Source is the service, stack or global policy level.
	Source string `json:"source"`
	// Enabled reports whether automatic drift repair is enabled.
	Enabled bool `json:"enabled"`
}

func prepare(original []byte, service string, stack *bool, global bool) (State, []byte, error) {
	state, target, err := inspect(original, service, stack, global)
	if err != nil || state.Enabled {
		return state, nil, err
	}
	updated, err := enableLabel(original, service, target)
	if err != nil {
		return State{}, nil, err
	}
	verified, _, err := inspect(updated, service, stack, global)
	if err != nil || !verified.Enabled {
		return State{}, nil, errors.New("could not verify edited Compose YAML")
	}
	return state, updated, nil
}

func inspect(original []byte, service string, stack *bool, global bool) (State, *yaml.Node, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(original))
	var doc, extra yaml.Node
	if err := decoder.Decode(&doc); err != nil || len(doc.Content) != 1 {
		return State{}, nil, errors.New("could not parse Compose YAML")
	}
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return State{}, nil, errors.New("expected a single Compose document")
	}
	root := doc.Content[0]
	services, err := localField(root, "services")
	if err != nil || services == nil {
		return State{}, nil, errors.New("expected a local services mapping")
	}
	target, err := localField(services, service)
	if err != nil || target == nil {
		return State{}, nil, fmt.Errorf("could not locate service %q in Compose", service)
	}
	var definition compose.Service
	if decodeErr := target.Decode(&definition); decodeErr != nil {
		return State{}, nil, errors.New("invalid Compose service definition")
	}
	level, enabled, err := policies.ResolveSelfHeal(definition, stack, global)
	if err != nil {
		return State{}, nil, errors.New("service self-heal label must have a literal boolean value")
	}
	return State{Source: level, Enabled: enabled}, target, nil
}

func enableLabel(original []byte, service string, target *yaml.Node) ([]byte, error) {
	deploy, err := localField(target, "deploy")
	if err != nil {
		return nil, err
	}
	if deploy == nil {
		return editCompose(original, []string{"services", service, "deploy"}, map[string]any{
			"labels": map[string]string{label: "true"},
		})
	}
	labels, err := localField(deploy, "labels")
	if err != nil {
		return nil, err
	}
	if labels == nil {
		return editCompose(original, []string{"services", service, "deploy", "labels"},
			map[string]string{label: "true"})
	}
	if labels.Anchor != "" || labels.Style&yaml.TaggedStyle != 0 {
		return nil, errors.New("expected local deploy labels without an explicit YAML tag or anchor")
	}
	if labels.Kind == yaml.SequenceNode {
		return enableListLabel(original, service, labels)
	}
	current, err := localField(labels, label)
	if err != nil {
		return nil, err
	}
	if current != nil {
		if validationErr := validateLabelValue(current); validationErr != nil {
			return nil, validationErr
		}
	}
	return editCompose(original, []string{"services", service, "deploy", "labels", label}, "true")
}

func enableListLabel(original []byte, service string, labels *yaml.Node) ([]byte, error) {
	found := -1
	for i, item := range labels.Content {
		if item.Kind != yaml.ScalarNode {
			return nil, errors.New("expected literal Compose deploy labels")
		}
		key, _, _ := strings.Cut(item.Value, "=")
		if key != label {
			continue
		}
		if found >= 0 {
			return nil, errors.New("duplicate self-heal label")
		}
		if err := validateLabelValue(item); err != nil {
			return nil, err
		}
		found = i
	}
	item := "-"
	if found >= 0 {
		item = strconv.Itoa(found)
	}
	return editCompose(original, []string{"services", service, "deploy", "labels", item}, label+"=true")
}

func validateLabelValue(node *yaml.Node) error {
	if node.Kind != yaml.ScalarNode || node.Anchor != "" || node.Style&yaml.TaggedStyle != 0 {
		return errors.New("expected a local self-heal label value without an explicit YAML tag or anchor")
	}
	return nil
}

func editCompose(original []byte, path []string, value any) ([]byte, error) {
	updated, err := yamledit.Set(original, path, value)
	if err != nil {
		return nil, fmt.Errorf("could not edit Compose YAML: %w", err)
	}
	return updated, nil
}

// localField prevents an edit through a shared anchor or the accidental shadowing
// of a merged mapping that also contains unrelated settings.
func localField(mapping *yaml.Node, key string) (*yaml.Node, error) {
	if mapping.Kind != yaml.MappingNode || mapping.Anchor != "" || mapping.Style&yaml.TaggedStyle != 0 {
		return nil, errors.New("expected a locally defined Compose mapping")
	}
	var local *yaml.Node
	var merged bool
	for i := 0; i < len(mapping.Content); i += 2 {
		field := mapping.Content[i]
		switch {
		case field.Tag == "!!merge":
			merged = true
		case field.Value == key:
			if local != nil {
				return nil, fmt.Errorf("duplicate Compose key %q", key)
			}
			local = mapping.Content[i+1]
		}
	}
	if local == nil && merged {
		var effective map[string]yaml.Node
		if err := mapping.Decode(&effective); err != nil {
			return nil, errors.New("invalid merged Compose mapping")
		}
		if _, inherited := effective[key]; inherited {
			return nil, fmt.Errorf("field %q is inherited from a YAML merge", key)
		}
	}
	return local, nil
}
