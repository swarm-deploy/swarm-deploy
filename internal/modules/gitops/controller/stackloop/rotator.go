package stackloop

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
)

const sopsSecretSuffix = ".sops"

// Rotator rewrites config and secret names based on file contents.
type Rotator struct {
}

// NewRotator builds a compose object rotator.
func NewRotator() *Rotator {
	return &Rotator{}
}

// Rotate mutates shared object names in-place when rotation is enabled.
// SOPS-encrypted secret files are decrypted before hashing and materialized
// temporarily for docker stack deploy.
func (f *Rotator) Rotate(
	file *compose.File,
	stackName string,
	hashLength int,
	includePath bool,
	sops config.SecretRotationSOPSSpec,
) (bool, []string, error) {
	baseDir := filepath.Dir(file.Path)
	changed := false

	configsChanged, err := f.applyObjectTypeRotation(
		file.Compose.Configs,
		stackName,
		baseDir,
		hashLength,
		includePath,
		nil,
	)
	if err != nil {
		return false, nil, fmt.Errorf("configs: %w", err)
	}
	if configsChanged {
		changed = true
	}

	materialized := make([]string, 0)
	secretsChanged, err := f.applyObjectTypeRotation(
		file.Compose.Secrets,
		stackName,
		baseDir,
		hashLength,
		includePath,
		func(object *compose.SharedObject, sourcePath string) ([]byte, string, error) {
			if !strings.HasSuffix(object.File, sopsSecretSuffix) {
				data, readErr := os.ReadFile(sourcePath)
				return data, "", readErr
			}
			if !sops.Enabled {
				return nil, "", fmt.Errorf(
					"secret file %s uses %s suffix but SOPS support is disabled",
					object.File,
					sopsSecretSuffix,
				)
			}

			data, decryptErr := decryptSOPS(sourcePath, sops.Age.KeyFile)
			if decryptErr != nil {
				return nil, "", decryptErr
			}

			materializedPath, materializeErr := materializeSecret(data)
			if materializeErr != nil {
				return nil, "", materializeErr
			}

			return data, materializedPath, nil
		},
	)
	if err != nil {
		cleanupMaterializedSecrets(materialized)
		return false, nil, fmt.Errorf("secrets: %w", err)
	}
	if secretsChanged {
		changed = true
	}

	for _, object := range file.Compose.Secrets {
		if object == nil || !strings.HasSuffix(object.File, sopsSecretSuffix) {
			continue
		}

		sourcePath := resolveObjectFilePath(baseDir, object.File)
		data, decryptErr := decryptSOPS(sourcePath, sops.Age.KeyFile)
		if decryptErr != nil {
			cleanupMaterializedSecrets(materialized)
			return false, nil, fmt.Errorf("secrets: decrypt %s: %w", object.File, decryptErr)
		}

		materializedPath, materializeErr := materializeSecret(data)
		if materializeErr != nil {
			cleanupMaterializedSecrets(materialized)
			return false, nil, fmt.Errorf("secrets: materialize %s: %w", object.File, materializeErr)
		}
		materialized = append(materialized, materializedPath)
		object.File = materializedPath
		changed = true
	}

	return changed, materialized, nil
}

type objectFileReader func(object *compose.SharedObject, sourcePath string) ([]byte, string, error)

func (f *Rotator) applyObjectTypeRotation(
	objects compose.SharedObjects,
	stackName string,
	baseDir string,
	hashLength int,
	includePath bool,
	reader objectFileReader,
) (bool, error) {
	changed := false
	for objectName, object := range objects {
		if object.External {
			continue
		}

		if object.File == "" {
			continue
		}

		sourcePath := resolveObjectFilePath(baseDir, object.File)
		var fileBytes []byte
		var err error

		if reader == nil {
			fileBytes, err = os.ReadFile(sourcePath)
		} else {
			fileBytes, _, err = reader(object, sourcePath)
		}
		if err != nil {
			return false, fmt.Errorf("read %s for rotation: %w", object.File, err)
		}

		rotatedName := f.buildRotatedObjectName(stackName, objectName, object.File, fileBytes, hashLength, includePath)
		if object.Name == rotatedName {
			continue
		}

		object.Name = rotatedName // @todo
		changed = true
	}

	return changed, nil
}

func decryptSOPS(path string, ageKeyFile string) ([]byte, error) {
	cmd := exec.Command("sops", "decrypt", "--input-type", "binary", "--output-type", "binary", path) //nolint:gosec
	cmd.Env = append(os.Environ(), "SOPS_AGE_KEY_FILE="+ageKeyFile)

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	out, err := cmd.Output()
	if err != nil {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			return nil, fmt.Errorf("run sops decrypt: %w", err)
		}
		return nil, fmt.Errorf("run sops decrypt: %w: %s", err, message)
	}

	return out, nil
}

func materializeSecret(data []byte) (string, error) {
	file, err := os.CreateTemp("", "swarm-deploy-sops-*")
	if err != nil {
		return "", fmt.Errorf("create temporary secret file: %w", err)
	}

	path := file.Name()
	if err = file.Chmod(0o600); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return "", fmt.Errorf("chmod temporary secret file: %w", err)
	}
	if _, err = file.Write(data); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return "", fmt.Errorf("write temporary secret file: %w", err)
	}
	if err = file.Close(); err != nil {
		_ = os.Remove(path)
		return "", fmt.Errorf("close temporary secret file: %w", err)
	}

	return path, nil
}

func cleanupMaterializedSecrets(paths []string) {
	for _, path := range paths {
		_ = os.Remove(path)
	}
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
