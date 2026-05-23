package compose

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

type File struct {
	RawMap   map[string]any `json:"-"`
	RawBytes []byte         `json:"-"`
	Compose  Compose        `json:"compose"`
}

type Compose struct {
	Services Services           `yaml:"services" json:"services"`
	Networks map[string]Network `yaml:"networks" json:"networks"`
	Configs  SharedObjects      `yaml:"configs" json:"configs"`
	Secrets  SharedObjects      `yaml:"secrets" json:"secrets"`
}

type ObjectRef struct {
	Source string `yaml:"source" json:"source"`
	Target string `yaml:"target" json:"target,omitempty"`
}

const envPairParts = 2

var rotatableObjectTypes = []string{"configs", "secrets"}

func Parse(raw []byte) (*Compose, error) {
	schema := Compose{}
	err := yaml.Unmarshal(raw, &schema)
	if err != nil {
		return nil, fmt.Errorf("decode compose schema: %w", err)
	}

	return &schema, nil
}

func (f *File) MarshalYAML() ([]byte, error) {
	payload, err := yaml.Marshal(f.RawMap)
	if err != nil {
		return nil, fmt.Errorf("marshal compose yaml: %w", err)
	}
	return payload, nil
}

func (f *File) ComputeDigest(composePath string) (string, error) {
	baseDir := filepath.Dir(composePath)
	hasher := sha256.New()
	hasher.Write(f.RawBytes)

	compute := func(objects map[string]SharedObject, objectType string) error {
		for _, object := range objects {
			if object.External {
				continue
			}

			if object.File != "" {
				continue
			}

			absPath := filepath.Join(baseDir, object.File)
			content, err := os.ReadFile(absPath)
			if err != nil {
				return fmt.Errorf("read %s file %s for digest: %w", objectType, absPath, err)
			}

			hasher.Write([]byte(objectType))
			hasher.Write([]byte(object.Name))
			hasher.Write([]byte(object.File))
			hasher.Write(content)
		}

		return nil
	}

	if err := compute(f.Compose.Configs, "configs"); err != nil {
		return "", fmt.Errorf("compute for configs: %w", err)
	}

	if err := compute(f.Compose.Secrets, "secrets"); err != nil {
		return "", fmt.Errorf("compute for secrets: %w", err)
	}

	return hex.EncodeToString(hasher.Sum(nil)), nil
}

func (f *File) ApplyObjectRotation(
	stackName string,
	composePath string,
	hashLength int,
	includePath bool,
) (bool, error) {
	baseDir := filepath.Dir(composePath)
	changed := false

	for _, objectType := range rotatableObjectTypes {
		typeChanged, err := f.applyObjectTypeRotation(
			objectType,
			stackName,
			baseDir,
			hashLength,
			includePath,
		)
		if err != nil {
			return false, err
		}
		if typeChanged {
			changed = true
		}
	}

	return changed, nil
}

func (f *File) applyObjectTypeRotation(
	objectType string,
	stackName string,
	baseDir string,
	hashLength int,
	includePath bool,
) (bool, error) {
	objects, hasObjects := asMap(f.RawMap[objectType])
	if !hasObjects {
		return false, nil
	}

	changed := false
	for objectName, object := range objects {
		objectMap, objectMapValid := asMap(object)
		if !objectMapValid {
			return false, fmt.Errorf("compose %s.%s must be a map", objectType, objectName)
		}
		if isExternalObject(objectMap) {
			continue
		}

		fileValue := asString(objectMap["file"])
		if fileValue == "" {
			continue
		}

		fileBytes, err := os.ReadFile(filepath.Join(baseDir, fileValue))
		if err != nil {
			return false, fmt.Errorf("read %s %s for rotation: %w", objectType, fileValue, err)
		}

		rotatedName := buildRotatedObjectName(stackName, objectName, fileValue, fileBytes, hashLength, includePath)
		if asString(objectMap["name"]) == rotatedName {
			continue
		}

		objectMap["name"] = rotatedName
		changed = true
	}

	return changed, nil
}

func buildRotatedObjectName(
	stackName string,
	objectName string,
	fileValue string,
	fileBytes []byte,
	hashLength int,
	includePath bool,
) string {
	sum := sha256.Sum256(fileBytes)
	hash := hex.EncodeToString(sum[:])

	if includePath {
		pathSum := sha256.Sum256([]byte(fileValue))
		hash += hex.EncodeToString(pathSum[:])
	}

	if hashLength > 0 && hashLength < len(hash) {
		hash = hash[:hashLength]
	}

	return fmt.Sprintf("%s-%s-%s", stackName, objectName, hash)
}

func resolveNetworkAliases(networks []string, namesByAlias map[string]Network) []string {
	if len(networks) == 0 {
		return nil
	}

	if len(namesByAlias) == 0 {
		return networks
	}

	out := make([]string, 0, len(networks))
	for _, network := range networks {
		resolved, ok := namesByAlias[network]
		if ok && resolved.Name != "" {
			out = append(out, resolved.Name)
			continue
		}
		out = append(out, network)
	}
	return out
}

func asMap(v any) (map[string]any, bool) {
	if v == nil {
		return nil, false
	}
	typed, ok := v.(map[string]any)
	if ok {
		return typed, true
	}

	typedIface, ok := v.(map[any]any)
	if !ok {
		return nil, false
	}

	out := make(map[string]any, len(typedIface))
	for k, value := range typedIface {
		out[asString(k)] = value
	}
	return out, true
}

func asString(v any) string {
	switch typed := v.(type) {
	case nil:
		return ""
	case string:
		return typed
	case fmt.Stringer:
		return typed.String()
	case int:
		return strconv.Itoa(typed)
	case int64:
		return strconv.FormatInt(typed, 10)
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case bool:
		if typed {
			return "true"
		}
		return "false"
	default:
		return ""
	}
}

func isExternalObject(objectMap map[string]any) bool {
	externalRaw, ok := objectMap["external"]
	if !ok {
		return false
	}
	switch typed := externalRaw.(type) {
	case bool:
		return typed
	case string:
		return strings.EqualFold(strings.TrimSpace(typed), "true")
	default:
		return false
	}
}

func ImageVersion(fullName string) string {
	if fullName == "" {
		return ""
	}
	if idx := strings.LastIndex(fullName, "@"); idx >= 0 && idx+1 < len(fullName) {
		return fullName[idx+1:]
	}
	lastSlash := strings.LastIndex(fullName, "/")
	lastColon := strings.LastIndex(fullName, ":")
	if lastColon > lastSlash && lastColon+1 < len(fullName) {
		return fullName[lastColon+1:]
	}
	return "latest"
}
