package stackloop

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"

	"github.com/swarm-deploy/swarm-deploy/internal/compose"
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

	apply := func(objects compose.SharedObjects) error {
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

	if err := apply(file.Compose.Configs); err != nil {
		return changed, fmt.Errorf("configs: %w", err)
	}

	if err := apply(file.Compose.Secrets); err != nil {
		return changed, fmt.Errorf("secrets: %w", err)
	}

	return changed, nil
}

func (f *Rotator) applyObjectTypeRotation(
	objects compose.SharedObjects,
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

		rotatedName := f.buildRotatedObjectName(stackName, objectName, object.File, fileBytes, hashLength, includePath)
		if object.File == rotatedName {
			continue
		}

		object.Name = rotatedName // @todo
		changed = true
	}

	return changed, nil
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
