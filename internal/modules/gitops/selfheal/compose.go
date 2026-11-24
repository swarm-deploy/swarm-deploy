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
	Source string
	// Enabled reports whether automatic drift repair is enabled.
	Enabled bool
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
	updated, err := enableLabel(original, target)
	return state, updated, err
}

func enableLabel(raw []byte, service *yaml.Node) ([]byte, error) {
	const entry = label + `: "true"`
	deploy, err := localField(service, "deploy")
	if err != nil {
		return nil, err
	}
	if deploy == nil {
		return insertEntry(raw, service, "deploy: {labels: {"+entry+"}}")
	}
	labels, err := localField(deploy, "labels")
	if err != nil {
		return nil, err
	}
	if labels == nil {
		return insertEntry(raw, deploy, "labels: {"+entry+"}")
	}
	if labels.Anchor != "" || labels.Style&yaml.TaggedStyle != 0 {
		return nil, errors.New("expected local deploy labels without an explicit YAML tag or anchor")
	}
	return setLabel(raw, labels)
}

func setLabel(raw []byte, labels *yaml.Node) ([]byte, error) {
	if labels.Kind == yaml.MappingNode {
		value, err := localField(labels, label)
		if err != nil {
			return nil, err
		}
		if value != nil {
			return replaceScalar(raw, value, `"true"`)
		}
		return insertEntry(raw, labels, label+`: "true"`)
	}
	if labels.Kind != yaml.SequenceNode {
		return nil, errors.New("expected a local deploy.labels mapping or list")
	}
	var found *yaml.Node
	for _, item := range labels.Content {
		key, _, _ := strings.Cut(item.Value, "=")
		if item.Kind != yaml.ScalarNode {
			return nil, errors.New("expected literal Compose deploy labels")
		}
		if key == label {
			if found != nil {
				return nil, errors.New("duplicate self-heal label")
			}
			found = item
		}
	}
	if found != nil {
		return replaceScalar(raw, found, label+"=true")
	}
	return insertEntry(raw, labels, label+"=true")
}
