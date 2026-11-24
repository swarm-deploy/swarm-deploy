package selfheal

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

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

// localField prevents an edit through a shared anchor or the accidental shadowing
// of a merged mapping that also contains unrelated settings.
func localField(mapping *yaml.Node, key string) (*yaml.Node, error) {
	if mapping.Kind != yaml.MappingNode || mapping.Anchor != "" || mapping.Style&yaml.TaggedStyle != 0 {
		return nil, errors.New("expected a locally defined Compose mapping")
	}
	var found *yaml.Node
	merged := false
	for i := 0; i < len(mapping.Content); i += 2 {
		field := mapping.Content[i]
		if field.Tag == "!!merge" {
			merged = true
			continue
		}
		if field.Value == key {
			if found != nil {
				return nil, fmt.Errorf("duplicate Compose key %q", key)
			}
			found = mapping.Content[i+1]
		}
	}
	if found == nil && merged {
		var effective map[string]yaml.Node
		if err := mapping.Decode(&effective); err != nil {
			return nil, errors.New("invalid merged Compose mapping")
		}
		if _, ok := effective[key]; ok {
			return nil, fmt.Errorf("field %q is inherited from a YAML merge", key)
		}
	}
	return found, nil
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

// Apply the edit at the parser's source coordinates to preserve comments, CRLF,
// formatting, and every byte belonging to other services.
func offsetAt(raw []byte, node *yaml.Node) (int, error) {
	pos := 0
	for line := 1; line < node.Line; line++ {
		index := bytes.IndexByte(raw[pos:], '\n')
		if index < 0 {
			return 0, errors.New("invalid YAML line position")
		}
		pos += index + 1
	}
	if node.Line == 1 {
		pos += len(raw[pos:]) - len(bytes.TrimPrefix(raw[pos:], []byte{0xef, 0xbb, 0xbf}))
	}
	for column := 1; column < node.Column && pos < len(raw); column++ {
		_, width := utf8.DecodeRune(raw[pos:])
		pos += width
	}
	return pos, nil
}

func insertEntry(raw []byte, parent *yaml.Node, entry string) ([]byte, error) {
	pos, err := offsetAt(raw, parent)
	if err != nil {
		return nil, err
	}
	if parent.Style&yaml.FlowStyle != 0 {
		if pos >= len(raw) || (raw[pos] != '{' && raw[pos] != '[') {
			return nil, errors.New("unsupported tagged YAML collection")
		}
		if len(parent.Content) > 0 {
			entry += ", "
		}
		return splice(raw, pos+1, pos+1, entry), nil
	}
	if parent.Kind == yaml.SequenceNode {
		entry = "- " + entry
	}
	newline := "\n"
	if bytes.Contains(raw, []byte("\r\n")) {
		newline = "\r\n"
	}
	return splice(raw, pos, pos, entry+newline+strings.Repeat(" ", parent.Column-1)), nil
}

func replaceScalar(raw []byte, value *yaml.Node, replacement string) ([]byte, error) {
	if value.Kind != yaml.ScalarNode || value.Anchor != "" {
		return nil, errors.New("expected a local self-heal label value")
	}
	pos, err := offsetAt(raw, value)
	if err != nil {
		return nil, err
	}
	literal := value.Value
	switch value.Style { //nolint:exhaustive // Only plain or single-line quoted label values can be replaced verbatim.
	case yaml.SingleQuotedStyle:
		literal = "'" + literal + "'"
	case yaml.DoubleQuotedStyle:
		literal = `"` + literal + `"`
	case 0:
	default:
		return nil, errors.New("expected a single-line literal self-heal label")
	}
	if !bytes.HasPrefix(raw[pos:], []byte(literal)) || strings.ContainsAny(literal, "\r\n") {
		return nil, errors.New("could not locate the literal self-heal label")
	}
	return splice(raw, pos, pos+len(literal), replacement), nil
}

func splice(raw []byte, start, end int, replacement string) []byte {
	var out bytes.Buffer
	out.Grow(len(raw) + len(replacement))
	out.Write(raw[:start])
	out.WriteString(replacement)
	out.Write(raw[end:])
	return out.Bytes()
}
