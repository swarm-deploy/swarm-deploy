package compose

import (
	"fmt"
	"reflect"
	"strings"

	"go.yaml.in/yaml/v3"
)

const yamlNullTag = "!!null"

// validateComposeSyntax rejects unsupported keys before YAML decoding can silently
// discard them. Extension fields and Swarm-ignored service options are deliberately
// excluded from the effective state, not retained as arbitrary model data.
func validateComposeSyntax(node *yaml.Node) ([]string, error) {
	var ignored []string
	if err := checkComposeFields(node, reflect.TypeFor[Compose](), "compose", &ignored); err != nil {
		return nil, err
	}
	return ignored, nil
}

func checkComposeFields(node *yaml.Node, typ reflect.Type, path string, ignored *[]string) error {
	if node == nil {
		return nil
	}
	if node.Kind == yaml.DocumentNode {
		if len(node.Content) == 0 {
			return nil
		}
		return checkComposeFields(node.Content[0], typ, path, ignored)
	}
	if node.Kind == yaml.AliasNode {
		return checkComposeFields(node.Alias, typ, path, ignored)
	}
	if node.Kind == yaml.ScalarNode || node.Tag == yamlNullTag {
		return nil // The YAML decoder validates scalar types.
	}
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}

	switch typ {
	case reflect.TypeFor[Services]():
		return checkMappedComposeValues(node, reflect.TypeFor[Service](), path, ignored)
	case reflect.TypeFor[ServiceNetworks]():
		return checkMappedComposeValues(node, reflect.TypeFor[ServiceNetwork](), path, ignored)
	case reflect.TypeFor[ServiceVolumes]():
		return checkSequenceComposeValues(node, reflect.TypeFor[serviceVolumeSchema](), path, ignored)
	case reflect.TypeFor[ServicePorts]():
		return checkSequenceComposeValues(node, reflect.TypeFor[ServicePort](), path, ignored)
	case reflect.TypeFor[Environment](), reflect.TypeFor[Labels](),
		reflect.TypeFor[EnvFiles](), reflect.TypeFor[Command](),
		reflect.TypeFor[StringList](), reflect.TypeFor[StringMap](),
		reflect.TypeFor[HostsList]():
		return nil // These custom forms accept arbitrary mapping keys or scalar/list syntax.
	}

	switch typ.Kind() {
	case reflect.Map:
		return checkMappedComposeValues(node, typ.Elem(), path, ignored)
	case reflect.Slice, reflect.Array:
		return checkSequenceComposeValues(node, typ.Elem(), path, ignored)
	case reflect.Struct:
		return checkComposeStructFields(node, typ, path, ignored)
	default:
		return nil
	}
}

func checkComposeStructFields(node *yaml.Node, typ reflect.Type, path string, ignored *[]string) error {
	if node.Kind != yaml.MappingNode {
		return nil // The YAML decoder reports malformed shapes.
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		key, value := node.Content[i].Value, node.Content[i+1]
		if key == "<<" {
			if err := checkComposeMerge(value, typ, path, ignored); err != nil {
				return err
			}
			continue
		}
		fieldType, exists := composeFieldType(typ, key)
		if !exists {
			if err := checkUnknownComposeField(typ, path, key, ignored); err != nil {
				return err
			}
			continue
		}
		if err := checkComposeFields(value, fieldType, path+"."+key, ignored); err != nil {
			return err
		}
	}
	return nil
}

func checkUnknownComposeField(typ reflect.Type, path, key string, ignored *[]string) error {
	if strings.HasPrefix(key, "x-") || (typ == reflect.TypeFor[Compose]() && key == "version") {
		return nil
	}
	if typ == reflect.TypeFor[Service]() && IsSwarmIgnoredOption(key) {
		*ignored = append(*ignored, path+"."+key)
		return nil
	}
	return fmt.Errorf("%s.%s: unsupported Compose option", path, key)
}

func checkMappedComposeValues(node *yaml.Node, typ reflect.Type, path string, ignored *[]string) error {
	if node.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if strings.HasPrefix(node.Content[i].Value, "x-") && typ == reflect.TypeFor[Service]() {
			continue
		}
		if err := checkComposeFields(node.Content[i+1], typ, path+"."+node.Content[i].Value, ignored); err != nil {
			return err
		}
	}
	return nil
}

func checkSequenceComposeValues(node *yaml.Node, typ reflect.Type, path string, ignored *[]string) error {
	if node.Kind != yaml.SequenceNode {
		return nil
	}
	for i, child := range node.Content {
		if err := checkComposeFields(child, typ, fmt.Sprintf("%s[%d]", path, i), ignored); err != nil {
			return err
		}
	}
	return nil
}

func checkComposeMerge(node *yaml.Node, typ reflect.Type, path string, ignored *[]string) error {
	if node.Kind == yaml.SequenceNode {
		return checkSequenceComposeValues(node, typ, path, ignored)
	}
	return checkComposeFields(node, typ, path, ignored)
}

func composeFieldType(typ reflect.Type, key string) (reflect.Type, bool) {
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		if !field.IsExported() {
			continue
		}
		name := strings.Split(field.Tag.Get("yaml"), ",")[0]
		if name == "-" {
			continue
		}
		if name == "" {
			name = strings.ToLower(field.Name)
		}
		if name == key {
			return field.Type, true
		}
	}
	return nil, false
}

// setIgnoredOptions attaches non-persistent warnings to the parsed services.
func setIgnoredOptions(desired *Compose, paths []string) {
	for _, path := range paths {
		parts := strings.Split(path, ".")
		if len(parts) != 4 || parts[0] != "compose" || parts[1] != "services" {
			continue
		}
		for i := range desired.Services {
			if desired.Services[i].Name == parts[2] {
				desired.Services[i].IgnoredOptions = append(desired.Services[i].IgnoredOptions, parts[3])
				break
			}
		}
	}
}
