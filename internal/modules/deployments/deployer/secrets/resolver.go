// Package secrets loads and decodes transient deployment secret payloads.
package secrets

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"sort"

	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/fs"
)

// SecretDecoder decodes one supported secret file format.
type SecretDecoder interface {
	// Matches reports whether the decoder handles a source path.
	Matches(path string) bool
	// Decode returns owned plaintext bytes or a diagnostic containing no sensitive data.
	Decode(content []byte) ([]byte, error)
}

// PlaintextDecoder handles ordinary secret files as the final decoder fallback.
type PlaintextDecoder struct{}

// Matches accepts any source path.
func (PlaintextDecoder) Matches(string) bool { return true }

// Decode copies the file contents without changing their value.
func (PlaintextDecoder) Decode(content []byte) ([]byte, error) { return bytes.Clone(content), nil }

// SecretResolver loads and validates secret files before Docker resource reconciliation.
type SecretResolver struct {
	fileSystem fs.FileSystem
	decoders   []SecretDecoder
}

// NewSecretResolver selects the first matching decoder, in the supplied order.
func NewSecretResolver(fileSystem fs.FileSystem, decoders ...SecretDecoder) *SecretResolver {
	return &SecretResolver{fileSystem: fileSystem, decoders: decoders}
}

// Resolve returns transient plaintext indexed by Compose alias, including for reused Secrets.
// External and driver-backed Secrets have no local payload. The caller clears successful results.
func (r *SecretResolver) Resolve(
	ctx context.Context,
	stackName, sourceComposePath string,
	objects compose.Secrets,
) (map[string][]byte, error) {
	resolved := make(map[string][]byte, len(objects))
	for _, alias := range sortedAliases(objects) {
		object := objects[alias]
		if object.External {
			continue
		}
		if object.Driver != "" && object.File == "" {
			continue
		}
		data, err := r.resolveFile(ctx, sourceComposePath, object)
		if err != nil {
			Clear(resolved)
			return nil, fmt.Errorf("read secret %s: %w", dockerSecretName(stackName, alias, object), err)
		}
		resolved[alias] = data
	}
	return resolved, nil
}

func (r *SecretResolver) resolveFile(
	ctx context.Context,
	sourceComposePath string,
	object *compose.Secret,
) ([]byte, error) {
	if object.Driver != "" {
		return nil, fmt.Errorf("cannot combine a secret file with an external driver")
	}
	if object.File == "" {
		return nil, fmt.Errorf("file is required")
	}
	path := object.File
	if !filepath.IsAbs(path) {
		path = filepath.Join(filepath.Dir(sourceComposePath), path)
	}
	content, err := r.fileSystem.ReadFile(ctx, path)
	if err != nil {
		return nil, err
	}
	for _, decoder := range r.decoders {
		if decoder.Matches(path) {
			return decoder.Decode(content)
		}
	}
	return nil, fmt.Errorf("no decoder for secret file")
}

// Clear overwrites all transient plaintext buffers in place.
func Clear(data map[string][]byte) {
	for _, value := range data {
		clear(value)
	}
}

func sortedAliases(objects compose.Secrets) []string {
	aliases := make([]string, 0, len(objects))
	for alias := range objects {
		aliases = append(aliases, alias)
	}
	sort.Strings(aliases)
	return aliases
}

func dockerSecretName(stackName, alias string, object *compose.Secret) string {
	if object.Name != "" {
		return object.Name
	}
	if object.External {
		return alias
	}
	return stackName + "_" + alias
}
