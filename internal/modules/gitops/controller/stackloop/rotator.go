package stackloop

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"

	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/labelsdict"
)

// Rotator rewrites config and secret names based on file contents.
type Rotator struct {
}

// NewRotator builds a compose object rotator.
func NewRotator() *Rotator {
	return &Rotator{}
}

// Rotate mutates shared object names in-place when rotation is enabled.
func (f *Rotator) Rotate(
	file *compose.File,
	stackName string,
	hashLength int,
	includePath bool,
) (bool, error) {
	baseDir := filepath.Dir(file.Path)
	changed := false

	apply := func(typeChanged bool, err error) error {
		if err != nil {
			return err
		}
		if typeChanged {
			changed = true
		}
		return nil
	}

	if err := apply(applyObjectTypeRotation(
		f,
		file.Compose.Configs,
		stackName,
		baseDir,
		hashLength,
		includePath,
		func(config *compose.Config) (string, bool) { return config.File, config.External },
		func(config *compose.Config, name, objectName string) {
			config.Name = name
			config.Labels.Add(labelsdict.RotatedResourceManagedLabelKey, labelsdict.RotatedResourceManagedLabelValue)
			config.Labels.Add(labelsdict.RotatedResourceLogicalNameLabelKey, objectName)
		},
	)); err != nil {
		return changed, fmt.Errorf("configs: %w", err)
	}

	if err := apply(applyObjectTypeRotation(
		f,
		file.Compose.Secrets,
		stackName,
		baseDir,
		hashLength,
		includePath,
		func(secret *compose.Secret) (string, bool) { return secret.File, secret.External },
		func(secret *compose.Secret, name, objectName string) {
			secret.Name = name
			secret.Labels.Add(labelsdict.RotatedResourceManagedLabelKey, labelsdict.RotatedResourceManagedLabelValue)
			secret.Labels.Add(labelsdict.RotatedResourceLogicalNameLabelKey, objectName)
		},
	)); err != nil {
		return changed, fmt.Errorf("secrets: %w", err)
	}

	return changed, nil
}

func applyObjectTypeRotation[T any, Objects ~map[string]*T](
	f *Rotator,
	objects Objects,
	stackName string,
	baseDir string,
	hashLength int,
	includePath bool,
	properties func(*T) (file string, external bool),
	apply func(*T, string, string),
) (bool, error) {
	changed := false
	for objectName, object := range objects {
		objectFile, external := properties(object)
		if external {
			continue
		}

		if objectFile == "" {
			continue
		}

		fileBytes, err := os.ReadFile(resolveObjectFilePath(baseDir, objectFile))
		if err != nil {
			return false, fmt.Errorf("read %s for rotation: %w", objectFile, err)
		}

		rotatedName := f.buildRotatedObjectName(stackName, objectName, objectFile, fileBytes, hashLength, includePath)
		if objectFile == rotatedName {
			continue
		}

		apply(object, rotatedName, objectName)
		changed = true
	}

	return changed, nil
}

// DesiredResourceNames resolves current rotated Docker names keyed by logical compose object name.
func (f *Rotator) DesiredResourceNames(
	file *compose.File,
	stackName string,
	hashLength int,
	includePath bool,
) (map[string]string, map[string]string, error) {
	baseDir := filepath.Dir(file.Path)

	resolveConfigs := func(objects compose.Configs) (map[string]string, error) {
		resolved := make(map[string]string)
		for objectName, object := range objects {
			if object.External || object.File == "" {
				continue
			}
			if object.Name != "" &&
				object.Labels.Map[labelsdict.RotatedResourceManagedLabelKey] == labelsdict.RotatedResourceManagedLabelValue &&
				object.Labels.Map[labelsdict.RotatedResourceLogicalNameLabelKey] == objectName {
				resolved[objectName] = object.Name
				continue
			}

			fileBytes, err := os.ReadFile(resolveObjectFilePath(baseDir, object.File))
			if err != nil {
				return nil, fmt.Errorf("read %s for rotation: %w", object.File, err)
			}

			resolved[objectName] = f.buildRotatedObjectName(
				stackName,
				objectName,
				object.File,
				fileBytes,
				hashLength,
				includePath,
			)
		}

		return resolved, nil
	}

	resolveSecrets := func(objects compose.Secrets) (map[string]string, error) {
		resolved := make(map[string]string)
		for objectName, object := range objects {
			if object.External || object.File == "" {
				continue
			}
			if object.Name != "" &&
				object.Labels.Map[labelsdict.RotatedResourceManagedLabelKey] == labelsdict.RotatedResourceManagedLabelValue &&
				object.Labels.Map[labelsdict.RotatedResourceLogicalNameLabelKey] == objectName {
				resolved[objectName] = object.Name
				continue
			}

			fileBytes, err := os.ReadFile(resolveObjectFilePath(baseDir, object.File))
			if err != nil {
				return nil, fmt.Errorf("read %s for rotation: %w", object.File, err)
			}

			resolved[objectName] = f.buildRotatedObjectName(
				stackName,
				objectName,
				object.File,
				fileBytes,
				hashLength,
				includePath,
			)
		}

		return resolved, nil
	}

	configs, err := resolveConfigs(file.Compose.Configs)
	if err != nil {
		return nil, nil, fmt.Errorf("configs: %w", err)
	}
	secrets, err := resolveSecrets(file.Compose.Secrets)
	if err != nil {
		return nil, nil, fmt.Errorf("secrets: %w", err)
	}

	return configs, secrets, nil
}

func resolveObjectFilePath(baseDir string, filePath string) string {
	if filepath.IsAbs(filePath) {
		return filePath
	}

	return filepath.Join(baseDir, filePath)
}

func (*Rotator) buildRotatedObjectName(
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
