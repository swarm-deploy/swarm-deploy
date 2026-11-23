package imageupdate

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/distribution/reference"
	"go.yaml.in/yaml/v3"
)

func updateComposeImage(original []byte, service, version string) ([]byte, string, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(original))
	var document, extra yaml.Node
	if err := decoder.Decode(&document); err != nil {
		return nil, "", errors.New("could not parse Compose YAML")
	}
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) || len(document.Content) != 1 {
		return nil, "", errors.New("expected a single Compose YAML document")
	}
	services := mappingValue(document.Content[0], "services")
	target := mappingValue(services, service)
	image := mappingValue(target, "image")
	if image == nil || image.Kind != yaml.ScalarNode || image.Tag != "!!str" || image.Anchor != "" {
		return nil, "", fmt.Errorf("service %q must declare its own literal image", service)
	}
	current := strings.TrimSpace(image.Value)
	parsed, err := reference.Parse(current)
	named, ok := parsed.(reference.Named)
	if err != nil || !ok {
		return nil, "", fmt.Errorf("service %q does not have a valid literal image reference", service)
	}
	tagged, err := reference.WithTag(reference.TrimNamed(named), version)
	if err != nil {
		return nil, "", errors.New("invalid image tag")
	}
	if tagged.String() == current {
		return nil, "", fmt.Errorf("service %q already uses image tag %q", service, version)
	}
	image.Value = tagged.String()
	var updated bytes.Buffer
	encoder := yaml.NewEncoder(&updated)
	const composeIndent = 2
	encoder.SetIndent(composeIndent)
	if encodeErr := encoder.Encode(&document); encodeErr != nil {
		return nil, "", errors.New("could not encode updated Compose YAML")
	}
	return updated.Bytes(), tagged.String(), nil
}

func mappingValue(mapping *yaml.Node, key string) *yaml.Node {
	if mapping == nil || mapping.Kind != yaml.MappingNode || mapping.Anchor != "" {
		return nil
	}
	var result *yaml.Node
	for i := 0; i < len(mapping.Content); i += 2 {
		name := mapping.Content[i]
		if name.Kind != yaml.ScalarNode || name.Value != key {
			continue
		}
		if result != nil {
			return nil
		}
		result = mapping.Content[i+1]
	}
	return result
}
