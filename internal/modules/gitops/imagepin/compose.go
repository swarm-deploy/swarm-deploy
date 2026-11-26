package imagepin

import (
	"bytes"
	"errors"
	"fmt"
	"io"

	"github.com/swarm-deploy/swarm-deploy/internal/shared/yamledit"
	"go.yaml.in/yaml/v3"
)

func inspect(original []byte, service string) (string, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(original))
	var doc, extra yaml.Node
	if err := decoder.Decode(&doc); err != nil || len(doc.Content) != 1 {
		return "", errors.New("could not parse Compose YAML")
	}
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return "", errors.New("expected a single Compose document")
	}
	services, err := localField(doc.Content[0], "services")
	if err != nil || services == nil {
		return "", errors.New("expected a local services mapping")
	}
	target, err := localField(services, service)
	if err != nil || target == nil {
		return "", fmt.Errorf("could not locate service %q in Compose", service)
	}
	image, err := localField(target, "image")
	if err != nil {
		return "", err
	}
	if image == nil {
		return "", fmt.Errorf("service %q has no local image field", service)
	}
	if image.Kind != yaml.ScalarNode || image.Anchor != "" || image.Style&yaml.TaggedStyle != 0 {
		return "", errors.New("expected a literal local Compose image value")
	}
	if image.Value == "" {
		return "", errors.New("compose image value is empty")
	}
	return image.Value, nil
}

func setImage(original []byte, service, image string) ([]byte, error) {
	updated, err := yamledit.Set(original, []string{"services", service, "image"}, image)
	if err != nil {
		return nil, fmt.Errorf("could not edit Compose YAML: %w", err)
	}
	verified, err := inspect(updated, service)
	if err != nil || verified != image {
		return nil, errors.New("could not verify edited Compose YAML")
	}
	return updated, nil
}

// localField prevents edits through shared anchors or accidental shadowing of
// fields inherited from merged mappings.
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
