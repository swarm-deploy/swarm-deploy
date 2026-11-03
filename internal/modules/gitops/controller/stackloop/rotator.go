package stackloop

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/labelsdict"
)

// Rotator rewrites config and secret names based on file contents.
type Rotator struct{}

// NewRotator builds a compose object rotator.
func NewRotator() *Rotator {
	return &Rotator{}
}

// Rotate mutates shared object names in-place when rotation is enabled.
func (f *Rotator) Rotate(
	_ context.Context,
	file *compose.File,
	stackName string,
	hashLength int,
	includePath bool,
) (bool, error) {
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
		hashLength,
		includePath,
		func(config *compose.Config) (string, bool, []byte) { return config.File, config.External, config.Data },
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
		hashLength,
		includePath,
		func(secret *compose.Secret) (string, bool, []byte) { return secret.File, secret.External, secret.Data },
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
	hashLength int,
	includePath bool,
	properties func(*T) (file string, external bool, content []byte),
	apply func(*T, string, string),
) (bool, error) {
	changed := false
	for objectName, object := range objects {
		objectFile, external, fileBytes := properties(object)
		if external {
			continue
		}

		if objectFile == "" {
			continue
		}

		if fileBytes == nil {
			return false, fmt.Errorf("file %s was not loaded for rotation", objectFile)
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
	_ context.Context,
	file *compose.File,
	stackName string,
	hashLength int,
	includePath bool,
) (map[string]string, map[string]string, error) {

	configs, err := resolveDesiredResourceNames(
		f, file.Compose.Configs, stackName, hashLength, includePath,
		func(config *compose.Config) (string, string, bool, compose.Labels, []byte) {
			return config.Name, config.File, config.External, config.Labels, config.Data
		},
	)
	if err != nil {
		return nil, nil, fmt.Errorf("configs: %w", err)
	}
	secrets, err := resolveDesiredResourceNames(
		f, file.Compose.Secrets, stackName, hashLength, includePath,
		func(secret *compose.Secret) (string, string, bool, compose.Labels, []byte) {
			return secret.Name, secret.File, secret.External, secret.Labels, secret.Data
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
	hashLength int,
	includePath bool,
	properties func(*T) (name string, file string, external bool, labels compose.Labels, content []byte),
) (map[string]string, error) {
	resolved := make(map[string]string)
	for objectName, object := range objects {
		name, objectFile, external, labels, fileBytes := properties(object)
		if external || objectFile == "" {
			continue
		}
		if isManagedRotatedResource(name, objectName, labels) {
			resolved[objectName] = name
			continue
		}

		if fileBytes == nil {
			return nil, fmt.Errorf("file %s was not loaded for rotation", objectFile)
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
