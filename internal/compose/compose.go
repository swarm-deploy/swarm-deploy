package compose

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/artarts36/specw"
	"gopkg.in/yaml.v3"
)

type ObjectRef struct {
	Source string `yaml:"source" json:"source"`
	Target string `yaml:"target" json:"target,omitempty"`
}

type InitJob struct {
	Name        string         `yaml:"name" json:"name"`
	Image       string         `yaml:"image" json:"image"`
	Command     []string       `yaml:"command" json:"command"`
	Environment Environment    `yaml:"environment" json:"environment,omitempty"`
	Networks    []string       `yaml:"networks" json:"networks,omitempty"`
	Secrets     []ObjectRef    `yaml:"secrets" json:"secrets,omitempty"`
	Configs     []ObjectRef    `yaml:"configs" json:"configs,omitempty"`
	Timeout     specw.Duration `yaml:"timeout" json:"timeout,omitempty"`
}

type Service struct {
	Name        string      `yaml:"name" json:"name"`
	Image       string      `yaml:"image" json:"image"`
	Environment Environment `yaml:"environment" json:"environment,omitempty"`
	Networks    []string    `yaml:"networks" json:"networks,omitempty"`
	Secrets     []ObjectRef `yaml:"secrets" json:"secrets,omitempty"`
	Configs     []ObjectRef `yaml:"configs" json:"configs,omitempty"`
	InitJobs    []InitJob   `yaml:"x-init-deploy-jobs" json:"init_jobs,omitempty"`
}

type File struct {
	RawMap   map[string]any     `json:"-"`
	RawBytes []byte             `json:"-"`
	Services map[string]Service `yaml:"services" json:"services"`
	Networks map[string]Network `yaml:"networks" json:"networks"`
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

	schema := File{}
	err = yaml.Unmarshal(raw, &schema)
	if err != nil {
		return nil, fmt.Errorf("decode compose schema: %w", err)
	}

	err = linkServices(&schema)
	if err != nil {
		return nil, fmt.Errorf("link: %w", err)
	}

	schema.RawBytes = raw
	schema.RawMap = root

	return &schema, nil
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

func linkServices(file *File) error {
	networkNames := parseTopLevelNetworkNames(file.Networks)

	for name, service := range file.Services {
		service.Name = name
		service.Networks = resolveNetworkAliases(service.Networks, networkNames)
		service.Secrets = normalizeObjectRefs(service.Secrets)
		service.Configs = normalizeObjectRefs(service.Configs)

		initJobs, err := normalizeInitJobs(service.InitJobs, networkNames)
		if err != nil {
			return fmt.Errorf("parse services.%s.x-init-deploy-jobs: %w", name, err)
		}
		service.InitJobs = initJobs

		file.Services[name] = service
	}

	return nil
}

func parseTopLevelNetworkNames(networkDefinitions map[string]Network) map[string]string {
	if len(networkDefinitions) == 0 {
		return nil
	}

	out := make(map[string]string, len(networkDefinitions))
	for alias, networkDefinition := range networkDefinitions {
		resolved := alias
		if networkDefinition.Name != "" {
			resolved = networkDefinition.Name
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

func normalizeInitJobs(jobs []InitJob, networkNames map[string]string) ([]InitJob, error) {
	for i := range jobs {
		if jobs[i].Name == "" {
			jobs[i].Name = fmt.Sprintf("job-%d", i)
		}
		if jobs[i].Image == "" {
			return nil, fmt.Errorf("item %d image is required", i)
		}
		jobs[i].Networks = resolveNetworkAliases(jobs[i].Networks, networkNames)
		jobs[i].Secrets = normalizeObjectRefs(jobs[i].Secrets)
		jobs[i].Configs = normalizeObjectRefs(jobs[i].Configs)
	}
	return jobs, nil
}

func normalizeObjectRefs(refs []ObjectRef) []ObjectRef {
	if len(refs) == 0 {
		return nil
	}

	out := make([]ObjectRef, 0, len(refs))
	for _, ref := range refs {
		if ref.Source == "" {
			continue
		}
		out = append(out, ref)
	}

	if len(out) == 0 {
		return nil
	}

	return out
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
