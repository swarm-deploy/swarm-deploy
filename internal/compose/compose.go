package compose

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

type Compose struct {
	Services Services           `yaml:"services" json:"services"`
	Networks map[string]Network `yaml:"networks" json:"networks"`
	Configs  SharedObjects      `yaml:"configs" json:"configs"`
	Secrets  SharedObjects      `yaml:"secrets" json:"secrets"`
}

const envPairParts = 2

func Parse(raw []byte) (*Compose, error) {
	schema := Compose{}
	err := yaml.Unmarshal(raw, &schema)
	if err != nil {
		return nil, fmt.Errorf("decode compose schema: %w", err)
	}

	return &schema, nil
}

func (f *File) MarshalYAML() ([]byte, error) {
	payload, err := yaml.Marshal(f.Compose)
	if err != nil {
		return nil, fmt.Errorf("marshal compose yaml: %w", err)
	}
	return payload, nil
}

func (f *File) ApplyObjectRotation(
	stackName string,
	composePath string,
	hashLength int,
	includePath bool,
) (bool, error) {
	baseDir := filepath.Dir(composePath)
	changed := false

	apply := func(objects SharedObjects) error {
		typeChanged, err := f.applyObjectTypeRotation(
			objects,
			stackName,
			baseDir,
			hashLength,
			includePath,
		)
		if err != nil {
			return err
		}
		if typeChanged {
			changed = true
		}
		return nil
	}

	if err := apply(f.Compose.Configs); err != nil {
		return changed, fmt.Errorf("configs: %w", err)
	}

	if err := apply(f.Compose.Secrets); err != nil {
		return changed, fmt.Errorf("secrets: %w", err)
	}

	return changed, nil
}

func (f *File) applyObjectTypeRotation(
	objects SharedObjects,
	stackName string,
	baseDir string,
	hashLength int,
	includePath bool,
) (bool, error) {
	changed := false
	for objectName, object := range objects {
		if object.External {
			continue
		}

		if object.File == "" {
			continue
		}

		fileBytes, err := os.ReadFile(filepath.Join(baseDir, object.File))
		if err != nil {
			return false, fmt.Errorf("read %s for rotation: %w", object.File, err)
		}

		rotatedName := buildRotatedObjectName(stackName, objectName, object.File, fileBytes, hashLength, includePath)
		if object.File == rotatedName {
			continue
		}

		object.Name = rotatedName // @todo
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
