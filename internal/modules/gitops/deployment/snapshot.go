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
	"unicode"

	"github.com/artarts36/envmasker"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
)

const (
	resourcePathParts = 2
	contentPathParts  = 3
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
		// ServiceVolumes.Map and ServiceNetworks.AliasMap duplicate their lists.
		if _, isVolumeList := node["Volumes"]; isVolumeList {
			delete(node, "Map")
		}
		if _, isNetworkList := node["List"]; isNetworkList {
			delete(node, "AliasMap")
			delete(node, "Names")
			delete(node, "Aliases")
		}
		for _, listKey := range []string{"Ports", "Volumes", "List", "cap_add", "cap_drop", "secrets", "configs"} {
			if list, ok := node[listKey].([]any); ok {
				sort.SliceStable(list, func(i, j int) bool {
					left, _ := json.Marshal(list[i])
					right, _ := json.Marshal(list[j])
					return bytes.Compare(left, right) < 0
				})
			}
		}
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
			childPath := joinPath(path, name)
			// Commands, entrypoints, healthchecks and init-job scripts are opaque.
			// Their syntax is unconstrained, so token-level secret detection cannot
			// make them safe for persistence or display.
			if isOpaqueExecutableField(name) {
				node[name] = p.sanitizeOpaque(child, childPath)
				continue
			}
			// Config file bytes are already excluded by Compose's JSON contract.
			node[name] = p.sanitize(child, childPath, name)
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

func (p *Prepared) sanitizeOpaque(value any, path string) any {
	switch node := value.(type) {
	case map[string]any:
		for name, child := range node {
			node[name] = p.sanitizeOpaque(child, joinPath(path, name))
		}
		return node
	case []any:
		for i, child := range node {
			node[i] = p.sanitizeOpaque(child, joinPath(path, strconv.Itoa(i)))
		}
		return node
	case string:
		p.record(path, node, envmasker.MaskValue, node != "")
		if node == "" {
			return node
		}
		return envmasker.MaskValue
	case nil:
		return nil
	default:
		raw := fmt.Sprint(node)
		p.record(path, raw, raw, false)
		return node
	}
}

func isOpaqueExecutableField(key string) bool {
	switch strings.ToLower(key) {
	case "command", "entrypoint", "test", "script", "scripts":
		return true
	default:
		return false
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
		change := publicChange(path)
		change.Redacted = before.Redacted[path] || after.Redacted[path]
		switch {
		case !hadOld:
			change.Operation = OperationAdded
		case !hasCurrent:
			change.Operation = OperationRemoved
		default:
			change.Operation = OperationChanged
		}
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

func publicChange(path string) Change {
	parts := strings.Split(path, "/")
	change := Change{ResourceType: "stack", ResourceName: "definition", Field: publicField(parts)}
	if len(parts) >= resourcePathParts {
		switch parts[0] {
		case "services":
			change.ResourceType, change.ResourceName = "service", parts[1]
			change.Field = publicField(parts[2:])
		case "configs":
			change.ResourceType, change.ResourceName = "config", parts[1]
			change.Field = publicField(parts[2:])
		case "secrets":
			change.ResourceType, change.ResourceName = "secret", parts[1]
			change.Field = publicField(parts[2:])
		case "resources":
			if len(parts) >= contentPathParts && (parts[1] == "configs" || parts[1] == "secrets") {
				change.ResourceType = strings.TrimSuffix(parts[1], "s")
				change.ResourceName = parts[2]
				change.Field = "content"
			}
		}
	}
	if change.Field == "" {
		change.Field = "definition"
	}
	return change
}

func publicField(parts []string) string {
	fields := make([]string, 0, len(parts))
	for _, part := range parts {
		switch part {
		case "Map", "Keys", "Args", "Extra", "Ports", "Volumes", "Names", "List", "AliasMap",
			"Alias", "ResolvedName":
			continue
		}
		if _, err := strconv.Atoi(part); err == nil {
			if len(fields) > 0 {
				fields[len(fields)-1] += "[" + part + "]"
			}
			continue
		}
		if strings.HasPrefix(part, "@") {
			if len(fields) > 0 {
				fields[len(fields)-1] += "[" + strings.ReplaceAll(strings.ReplaceAll(part[1:], "~1", "/"), "~0", "~") + "]"
			}
			continue
		}
		fields = append(fields, publicSegment(strings.ReplaceAll(strings.ReplaceAll(part, "~1", "/"), "~0", "~")))
	}
	return strings.Join(fields, ".")
}

func publicSegment(value string) string {
	if value == strings.ToUpper(value) {
		return value
	}
	runes := []rune(value)
	var result strings.Builder
	for i, current := range runes {
		if unicode.IsUpper(current) {
			previousLower := i > 0 && unicode.IsLower(runes[i-1])
			nextLower := i+1 < len(runes) && unicode.IsLower(runes[i+1])
			if i > 0 && (previousLower || nextLower) {
				result.WriteByte('_')
			}
			current = unicode.ToLower(current)
		}
		result.WriteRune(current)
	}
	return result.String()
}

func summarize(changes []Change) (ChangeSummary, ResourceSummary) {
	var summary ChangeSummary
	resources := map[string]map[string]bool{"service": {}, "config": {}, "secret": {}}
	for _, change := range changes {
		switch change.Operation {
		case OperationAdded:
			summary.Added++
		case OperationRemoved:
			summary.Removed++
		case OperationChanged:
			summary.Changed++
		}
		if change.Redacted {
			summary.Redacted++
		}
		if names, ok := resources[change.ResourceType]; ok {
			names[change.ResourceName] = true
		}
	}
	return summary, ResourceSummary{
		Services: len(resources["service"]), Configs: len(resources["config"]), Secrets: len(resources["secret"]),
	}
}

func (p *Prepared) sanitizeList(node []any, path, key string) any {
	used := map[string]int{}
	for i, child := range node {
		segment := listSegment(child, i)
		count := used[segment]
		used[segment] = count + 1
		if count > 0 {
			segment += fmt.Sprintf("#%d", count)
		}
		fieldKey := listFieldKey(node, i, key)
		node[i] = p.sanitize(child, joinPath(path, segment), fieldKey)
	}
	return node
}

func listSegment(child any, index int) string {
	object, ok := child.(map[string]any)
	if !ok {
		return strconv.Itoa(index)
	}
	switch {
	case object["name"] != nil:
		return fmt.Sprint(object["name"])
	case object["Target"] != nil:
		return "@" + fmt.Sprint(object["Target"])
	case object["target"] != nil:
		segment := "@" + fmt.Sprint(object["target"])
		if object["protocol"] != nil {
			segment += "/" + fmt.Sprint(object["protocol"])
		}
		return segment
	case object["Alias"] != nil:
		return "@" + fmt.Sprint(object["Alias"])
	default:
		return strconv.Itoa(index)
	}
}

func listFieldKey(node []any, index int, fallback string) string {
	if index == 0 {
		return fallback
	}
	previous, ok := node[index-1].(string)
	if !ok || !strings.HasPrefix(previous, "-") {
		return fallback
	}
	if _, masked := envmasker.Mask(previous, "test-value"); masked {
		return previous
	}
	return fallback
}
