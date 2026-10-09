package deployment

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"sort"
	"strconv"
	"strings"

	"github.com/artarts36/envmasker"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
)

// Prepare copies the effective Compose in memory and masks it before persistence.
// Source formatting, checkout paths and env-file ordering are not effective state.
// The caller must populate env files and apply transformations before calling it.
func Prepare(file compose.File) (Prepared, error) {
	resourceDigests := maps.Clone(file.ResourceDigests)
	if resourceDigests == nil {
		resourceDigests = map[string]string{}
	}
	for name, config := range file.Compose.Configs {
		if config != nil && config.Data != nil {
			resourceDigests["configs/"+name] = fingerprint(config.Data)
		}
	}
	payload, err := json.Marshal(file.Compose)
	if err != nil {
		return Prepared{}, fmt.Errorf("encode effective desired state: %w", err)
	}
	var tree map[string]any
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	if err = decoder.Decode(&tree); err != nil {
		return Prepared{}, err
	}
	normalize(tree)
	if services, ok := tree["services"].([]any); ok {
		sort.SliceStable(services, func(i, j int) bool {
			left, _ := services[i].(map[string]any)
			right, _ := services[j].(map[string]any)
			return fmt.Sprint(left["name"]) < fmt.Sprint(right["name"])
		})
	}
	identity := struct {
		// Compose is normalized effective state, temporarily held only in memory.
		Compose map[string]any `json:"Compose"`
		// Resources includes referenced file-content digests.
		Resources map[string]string `json:"Resources"`
	}{tree, resourceDigests}
	canonical, err := json.Marshal(identity)
	if err != nil {
		return Prepared{}, err
	}
	result := Prepared{Digest: fingerprint(canonical), Fields: map[string]string{},
		Fingerprints: map[string]string{}, Redacted: map[string]bool{}}
	result.sanitize(tree, "", "")
	safe, err := json.Marshal(tree)
	if err != nil {
		return Prepared{}, err
	}
	result.Definition = compose.File{Path: file.Path, Digest: file.Digest, ResourceDigests: resourceDigests}
	if err = json.Unmarshal(safe, &result.Definition.Compose); err != nil {
		return Prepared{}, err
	}
	for name, digest := range resourceDigests {
		path := "resources/" + name
		result.Fields[path] = "[content]"
		result.Fingerprints[path] = digest
		result.Redacted[path] = true
	}
	return result, nil
}

func normalize(value any) {
	switch node := value.(type) {
	case map[string]any:
		// Environment.Keys is parser ordering, not container state. EnvFiles have
		// already been merged into environment and can contain duplicate raw values.
		delete(node, "Keys")
		delete(node, "env_file")
		for _, child := range node {
			normalize(child)
		}
	case []any:
		for _, child := range node {
			normalize(child)
		}
	}
}

func (p *Prepared) sanitize(value any, path, key string) any {
	switch node := value.(type) {
	case map[string]any:
		for name, child := range node {
			// Opaque inline file data and shell scripts are not safe display values.
			// Config file bytes are already excluded by Compose's JSON contract.
			node[name] = p.sanitize(child, joinPath(path, name), name)
		}
		return node
	case []any:
		return p.sanitizeList(node, path, key)
	case string:
		safe, masked := envmasker.Mask(key, node)
		if strings.EqualFold(key, "content") || strings.EqualFold(key, "data") {
			safe, masked = envmasker.MaskValue, node != ""
		}
		p.record(path, node, safe, masked)
		return safe
	case nil:
		return nil
	default:
		raw := fmt.Sprint(node)
		p.record(path, raw, raw, false)
		return node
	}
}

func (p *Prepared) record(path, raw, safe string, masked bool) {
	p.Fields[path] = safe
	p.Fingerprints[path] = fingerprint([]byte(raw))
	if masked {
		p.Redacted[path] = true
	}
}

func fingerprint(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}

func joinPath(parent, child string) string {
	child = strings.ReplaceAll(strings.ReplaceAll(child, "~", "~0"), "/", "~1")
	if parent == "" {
		return child
	}
	return parent + "/" + child
}

// Compare returns field-level transitions; sensitive fields expose no plaintext.
func Compare(before, after Prepared) []Change {
	paths := map[string]bool{}
	for path := range before.Fields {
		paths[path] = true
	}
	for path := range after.Fields {
		paths[path] = true
	}
	ordered := make([]string, 0, len(paths))
	for path := range paths {
		ordered = append(ordered, path)
	}
	sort.Strings(ordered)
	changes := []Change{}
	for _, path := range ordered {
		old, hadOld := before.Fields[path]
		current, hasCurrent := after.Fields[path]
		if hadOld == hasCurrent && before.Fingerprints[path] == after.Fingerprints[path] {
			continue
		}
		change := Change{Path: path, Redacted: before.Redacted[path] || after.Redacted[path]}
		if change.Redacted {
			old, current = envmasker.MaskValue, envmasker.MaskValue
		}
		if hadOld {
			change.Before = &old
		}
		if hasCurrent {
			change.After = &current
		}
		changes = append(changes, change)
	}
	return changes
}

func (p *Prepared) sanitizeList(node []any, path, key string) any {
	for i, child := range node {
		segment := strconv.Itoa(i)
		if object, ok := child.(map[string]any); ok {
			if name, named := object["name"].(string); named && name != "" {
				segment = name
			}
		}
		fieldKey := key
		if i > 0 {
			if previous, ok := node[i-1].(string); ok && strings.HasPrefix(previous, "-") {
				if _, masked := envmasker.Mask(previous, "test-value"); masked {
					fieldKey = previous
				}
			}
		}
		node[i] = p.sanitize(child, joinPath(path, segment), fieldKey)
	}

	return node
}
