package compose

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type ObjectRef struct {
	Source string `json:"source"`
	Target string `json:"target,omitempty"`
}

type InitJob struct {
	Name        string            `json:"name"`
	Image       string            `json:"image"`
	Command     []string          `json:"command"`
	Environment map[string]string `json:"environment,omitempty"`
	Networks    []string          `json:"networks,omitempty"`
	Secrets     []ObjectRef       `json:"secrets,omitempty"`
	Configs     []ObjectRef       `json:"configs,omitempty"`
	Timeout     time.Duration     `json:"timeout,omitempty"`
}

type Service struct {
	Name        string            `json:"name"`
	Image       string            `json:"image"`
	Environment map[string]string `json:"environment,omitempty"`
	Networks    []string          `json:"networks,omitempty"`
	Secrets     []ObjectRef       `json:"secrets,omitempty"`
	Configs     []ObjectRef       `json:"configs,omitempty"`
	InitJobs    []InitJob         `json:"init_jobs,omitempty"`
}

type File struct {
	RawMap   map[string]any `json:"-"`
	RawBytes []byte         `json:"-"`
	Services []Service      `json:"services"`
}

const envPairParts = 2

var rotatableObjectTypes = []string{"configs", "secrets"}

func Load(path string) (*File, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read compose file %s: %w", path, err)
	}

	return Parse(raw)
}

func Parse(raw []byte) (*File, error) {
	root := map[string]any{}
	err := yaml.Unmarshal(raw, &root)
	if err != nil {
		return nil, fmt.Errorf("decode compose yaml: %w", err)
	}

	services, err := parseServices(root)
	if err != nil {
		return nil, err
	}

	return &File{
		RawMap:   root,
		RawBytes: raw,
		Services: services,
	}, nil
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

	for _, objectType := range rotatableObjectTypes {
		objects, hasObjects := asMap(f.RawMap[objectType])
		if !hasObjects {
			continue
		}

		names := mapKeys(objects)
		sort.Strings(names)

		for _, name := range names {
			objectMap, objectMapValid := asMap(objects[name])
			if !objectMapValid {
				return "", fmt.Errorf("compose %s.%s must be a map", objectType, name)
			}
			if isExternalObject(objectMap) {
				continue
			}

			fileValue := asString(objectMap["file"])
			if fileValue == "" {
				continue
			}

			absPath := filepath.Join(baseDir, fileValue)
			content, err := os.ReadFile(absPath)
			if err != nil {
				return "", fmt.Errorf("read %s file %s for digest: %w", objectType, absPath, err)
			}

			hasher.Write([]byte(objectType))
			hasher.Write([]byte(name))
			hasher.Write([]byte(fileValue))
			hasher.Write(content)
		}
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

func parseServices(root map[string]any) ([]Service, error) {
	servicesMap, servicesMapFound := asMap(root["services"])
	if !servicesMapFound {
		return nil, errors.New("compose file does not contain services map")
	}

	networkNames := parseTopLevelNetworkNames(root["networks"])

	names := mapKeys(servicesMap)
	sort.Strings(names)

	services := make([]Service, 0, len(names))
	for _, name := range names {
		serviceMap, serviceMapValid := asMap(servicesMap[name])
		if !serviceMapValid {
			return nil, fmt.Errorf("compose services.%s must be a map", name)
		}

		initJobs, err := parseInitJobs(serviceMap["x-init-deploy-jobs"], networkNames)
		if err != nil {
			return nil, fmt.Errorf("parse services.%s.x-init-deploy-jobs: %w", name, err)
		}

		services = append(services, Service{
			Name:        name,
			Image:       asString(serviceMap["image"]),
			Environment: parseEnvironment(serviceMap["environment"]),
			Networks:    resolveNetworkAliases(parseNetworks(serviceMap["networks"]), networkNames),
			Secrets:     parseObjectRefs(serviceMap["secrets"]),
			Configs:     parseObjectRefs(serviceMap["configs"]),
			InitJobs:    initJobs,
		})
	}

	return services, nil
}

func parseInitJobs(raw any, networkNames map[string]string) ([]InitJob, error) {
	if raw == nil {
		return nil, nil
	}

	items, itemsIsArray := raw.([]any)
	if !itemsIsArray {
		return nil, errors.New("must be an array")
	}

	jobs := make([]InitJob, 0, len(items))
	for i, item := range items {
		jobMap, itemIsMap := asMap(item)
		if !itemIsMap {
			return nil, fmt.Errorf("item %d must be map", i)
		}

		name := asString(jobMap["name"])
		if name == "" {
			name = fmt.Sprintf("job-%d", i)
		}

		image := asString(jobMap["image"])
		if image == "" {
			return nil, fmt.Errorf("item %d image is required", i)
		}

		command, err := parseCommand(jobMap["command"])
		if err != nil {
			return nil, fmt.Errorf("item %d command: %w", i, err)
		}

		timeout, err := parseTimeout(jobMap["timeout"])
		if err != nil {
			return nil, fmt.Errorf("item %d timeout: %w", i, err)
		}

		jobs = append(jobs, InitJob{
			Name:        name,
			Image:       image,
			Command:     command,
			Environment: parseEnvironment(jobMap["environment"]),
			Networks:    resolveNetworkAliases(parseNetworks(jobMap["networks"]), networkNames),
			Secrets:     parseObjectRefs(jobMap["secrets"]),
			Configs:     parseObjectRefs(jobMap["configs"]),
			Timeout:     timeout,
		})
	}

	return jobs, nil
}

func parseTimeout(v any) (time.Duration, error) {
	switch t := v.(type) {
	case nil:
		return 0, nil
	case int:
		return time.Duration(t) * time.Second, nil
	case int64:
		return time.Duration(t) * time.Second, nil
	case float64:
		return time.Duration(int64(t)) * time.Second, nil
	case string:
		if t == "" {
			return 0, nil
		}
		return time.ParseDuration(t)
	default:
		return 0, fmt.Errorf("unsupported type %T", v)
	}
}

func parseCommand(raw any) ([]string, error) {
	if raw == nil {
		return nil, nil
	}
	if s, ok := raw.(string); ok {
		return strings.Fields(s), nil
	}
	items, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("command must be string or array")
	}
	result := make([]string, 0, len(items))
	for i, item := range items {
		result = append(result, asString(item))
		if result[len(result)-1] == "" {
			return nil, fmt.Errorf("command[%d] must be string", i)
		}
	}
	return result, nil
}

func parseEnvironment(raw any) map[string]string {
	if raw == nil {
		return nil
	}

	out := map[string]string{}

	if pairs, ok := raw.([]any); ok {
		for _, pair := range pairs {
			chunks := strings.SplitN(asString(pair), "=", envPairParts)
			if len(chunks) == envPairParts {
				out[chunks[0]] = chunks[1]
			}
		}
		return out
	}

	if envMap, ok := asMap(raw); ok {
		for k, v := range envMap {
			out[k] = asString(v)
		}
	}

	if len(out) == 0 {
		return nil
	}
	return out
}

func parseNetworks(raw any) []string {
	if raw == nil {
		return nil
	}

	if list, ok := raw.([]any); ok {
		out := make([]string, 0, len(list))
		for _, item := range list {
			v := asString(item)
			if v != "" {
				out = append(out, v)
			}
		}
		return out
	}

	if mapValue, mapValueFound := asMap(raw); mapValueFound {
		out := mapKeys(mapValue)
		sort.Strings(out)
		return out
	}

	one := asString(raw)
	if one == "" {
		return nil
	}
	return []string{one}
}

func parseTopLevelNetworkNames(raw any) map[string]string {
	networkDefs, ok := asMap(raw)
	if !ok {
		return nil
	}

	out := make(map[string]string, len(networkDefs))
	for alias, networkRaw := range networkDefs {
		resolved := alias
		if networkMap, networkIsMap := asMap(networkRaw); networkIsMap {
			if name := asString(networkMap["name"]); name != "" {
				resolved = name
			}
		}
		out[alias] = resolved
	}

	if len(out) == 0 {
		return nil
	}
	return out
}

func resolveNetworkAliases(networks []string, namesByAlias map[string]string) []string {
	if len(networks) == 0 {
		return nil
	}

	if len(namesByAlias) == 0 {
		return networks
	}

	out := make([]string, 0, len(networks))
	for _, network := range networks {
		resolved, ok := namesByAlias[network]
		if ok && resolved != "" {
			out = append(out, resolved)
			continue
		}
		out = append(out, network)
	}
	return out
}

func parseObjectRefs(raw any) []ObjectRef {
	if raw == nil {
		return nil
	}

	items, itemsIsArray := raw.([]any)
	if !itemsIsArray {
		return nil
	}

	result := make([]ObjectRef, 0, len(items))
	for _, item := range items {
		if name := asString(item); name != "" {
			result = append(result, ObjectRef{Source: name})
			continue
		}

		entry, entryIsMap := asMap(item)
		if !entryIsMap {
			continue
		}
		source := asString(entry["source"])
		target := asString(entry["target"])
		if source == "" {
			source = asString(entry["secret"])
		}
		if source == "" {
			source = asString(entry["config"])
		}
		if source == "" {
			continue
		}
		result = append(result, ObjectRef{Source: source, Target: target})
	}

	return result
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

func mapKeys(v map[string]any) []string {
	keys := make([]string, 0, len(v))
	for key := range v {
		keys = append(keys, key)
	}
	return keys
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
