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

	configs, err := resolveDesiredResourceNames(
		f, file.Compose.Configs, stackName, baseDir, hashLength, includePath,
		func(config *compose.Config) (string, string, bool, compose.Labels) {
			return config.Name, config.File, config.External, config.Labels
		},
	)
	if err != nil {
		return nil, nil, fmt.Errorf("configs: %w", err)
	}
	secrets, err := resolveDesiredResourceNames(
		f, file.Compose.Secrets, stackName, baseDir, hashLength, includePath,
		func(secret *compose.Secret) (string, string, bool, compose.Labels) {
			return secret.Name, secret.File, secret.External, secret.Labels
		},
	)
	if err != nil {
		return nil, nil, fmt.Errorf("secrets: %w", err)
	}

	return configs, secrets, nil
}

func resolveDesiredResourceNames[T any, Objects ~map[string]*T](
	rotator *Rotator,
	objects Objects,
	stackName string,
	baseDir string,
	hashLength int,
	includePath bool,
	properties func(*T) (name string, file string, external bool, labels compose.Labels),
) (map[string]string, error) {
	resolved := make(map[string]string)
	for objectName, object := range objects {
		name, objectFile, external, labels := properties(object)
		if external || objectFile == "" {
			continue
		}
		if isManagedRotatedResource(name, objectName, labels) {
			resolved[objectName] = name
			continue
		}

		fileBytes, err := os.ReadFile(resolveObjectFilePath(baseDir, objectFile))
		if err != nil {
			return nil, fmt.Errorf("read %s for rotation: %w", objectFile, err)
		}

		resolved[objectName] = rotator.buildRotatedObjectName(
			stackName, objectName, objectFile, fileBytes, hashLength, includePath,
		)
	}

	return resolved, nil
}

func isManagedRotatedResource(name string, objectName string, labels compose.Labels) bool {
	return name != "" &&
		labels.Map[labelsdict.RotatedResourceManagedLabelKey] == labelsdict.RotatedResourceManagedLabelValue &&
		labels.Map[labelsdict.RotatedResourceLogicalNameLabelKey] == objectName
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
