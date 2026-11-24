package selfheal

import (
	"bytes"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"go.yaml.in/yaml/v3"
)

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

// YAML treats a CRLF pair as one break and also accepts Unicode line separators.
var yamlLineBreak = regexp.MustCompile("\r\n|[\r\n\u0085\u2028\u2029]")

// The parser counts columns in Unicode code points. The edit needs a byte offset.
func offsetAt(raw []byte, node *yaml.Node) (int, error) {
	if !utf8.Valid(raw) {
		return 0, errors.New("editing Compose requires UTF-8 source")
	}
	rest := bytes.TrimPrefix(raw, []byte("\ufeff"))
	for range node.Line - 1 {
		end := yamlLineBreak.FindIndex(rest)
		if end == nil {
			return 0, errors.New("invalid YAML line position")
		}
		rest = rest[end[1]:]
	}
	for range node.Column - 1 {
		_, width := utf8.DecodeRune(rest)
		if width == 0 {
			return 0, errors.New("invalid YAML column position")
		}
		rest = rest[width:]
	}
	return len(raw) - len(rest), nil
}

func insertEntry(raw []byte, parent *yaml.Node, entry string) ([]byte, error) {
	if parent.Style&yaml.FlowStyle != 0 {
		open := "{"
		if parent.Kind == yaml.SequenceNode {
			open = "["
		}
		if len(parent.Content) > 0 {
			entry += ", "
		}
		return editAt(raw, parent, open, open+entry)
	}
	if parent.Kind == yaml.SequenceNode {
		entry = "- " + entry
	}
	newline := string(yamlLineBreak.Find(raw))
	if newline == "" {
		newline = "\n"
	}
	return editAt(raw, parent, "", entry+newline+strings.Repeat(" ", parent.Column-1))
}

func replaceScalar(raw []byte, value *yaml.Node, replacement string) ([]byte, error) {
	if value.Kind != yaml.ScalarNode || value.Anchor != "" {
		return nil, errors.New("expected a local self-heal label value")
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
	if strings.ContainsAny(literal, "\r\n") {
		return nil, errors.New("expected a single-line literal self-heal label")
	}
	return editAt(raw, value, literal, replacement)
}

// Change only the indicated token, leaving unrelated YAML bytes untouched.
func editAt(raw []byte, node *yaml.Node, before, after string) ([]byte, error) {
	pos, err := offsetAt(raw, node)
	if err != nil {
		return nil, err
	}
	tail, ok := bytes.CutPrefix(raw[pos:], []byte(before))
	if !ok {
		return nil, errors.New("could not locate YAML token in source")
	}
	return slices.Concat(raw[:pos], []byte(after), tail), nil
}
