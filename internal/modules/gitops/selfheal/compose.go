package selfheal

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/controller/stackloop/policies"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/labelsdict"
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
	state := State{Source: level, Enabled: enabled}
	if enabled {
		return state, nil, nil
	}
	if editErr := enableLabel(target); editErr != nil {
		return State{}, nil, editErr
	}
	quoteFoldedScalars(&doc)
	updated, err := yaml.Marshal(&doc)
	if err != nil {
		return State{}, nil, errors.New("could not marshal Compose YAML")
	}
	return state, updated, nil
}

// yaml.v3 can introduce extra newlines when re-emitting folded scalars.
func quoteFoldedScalars(node *yaml.Node) {
	if node.Style&yaml.FoldedStyle != 0 {
		node.Style = node.Style&^yaml.FoldedStyle | yaml.DoubleQuotedStyle
	}
	for _, child := range node.Content {
		quoteFoldedScalars(child)
	}
}

func enableLabel(service *yaml.Node) error {
	deploy, err := localField(service, "deploy")
	if err != nil {
		return err
	}
	if deploy == nil {
		deploy = appendMapping(service, "deploy")
	}
	labels, err := localField(deploy, "labels")
	if err != nil {
		return err
	}
	if labels == nil {
		labels = appendMapping(deploy, "labels")
	}
	if labels.Anchor != "" || labels.Style&yaml.TaggedStyle != 0 {
		return errors.New("expected local deploy labels without an explicit YAML tag or anchor")
	}
	if labels.Kind == yaml.SequenceNode {
		return enableListLabel(labels)
	}
	current, err := localField(labels, label)
	if err != nil {
		return err
	}
	if current == nil {
		labels.Content = append(labels.Content, stringNode(label), stringNode("true"))
		return nil
	}
	return setLabelValue(current, "true")
}

func enableListLabel(labels *yaml.Node) error {
	var found *yaml.Node
	for _, item := range labels.Content {
		if item.Kind != yaml.ScalarNode {
			return errors.New("expected literal Compose deploy labels")
		}
		key, _, _ := strings.Cut(item.Value, "=")
		if key != label {
			continue
		}
		if found != nil {
			return errors.New("duplicate self-heal label")
		}
		found = item
	}
	if found == nil {
		labels.Content = append(labels.Content, stringNode(label+"=true"))
		return nil
	}
	return setLabelValue(found, label+"=true")
}

func setLabelValue(node *yaml.Node, value string) error {
	if node.Kind != yaml.ScalarNode || node.Anchor != "" || node.Style&yaml.TaggedStyle != 0 {
		return errors.New("expected a local self-heal label value without an explicit YAML tag or anchor")
	}
	node.Tag = "!!str"
	node.Value = value
	return nil
}

func appendMapping(parent *yaml.Node, key string) *yaml.Node {
	mapping := &yaml.Node{Kind: yaml.MappingNode}
	parent.Content = append(parent.Content, stringNode(key), mapping)
	return mapping
}

func stringNode(value string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value}
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
