package compose

import (
	"bytes"
	jsonv1 "encoding/json"
	"encoding/json/v2"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/swarm-deploy/swarm-deploy/internal/shared/utils"
)

// Canonicalize returns deterministic JSON for an effective Compose definition.
// It normalizes equivalent command, entrypoint and healthcheck syntax, capabilities,
// insignificant list ordering and Docker defaults of object references without
// mutating desired. Decoding already applies most of this normalization; it is
// repeated here for definitions built in code.
//
// The output feeds effective desired-state digests stored in existing deployment
// baselines. Any change to it changes those digests and starts one Deployment per
// affected stack on the next sync, which also runs every Init Job of the stack.
// Keep the output stable and update the persisted-digest tests deliberately.
func Canonicalize(desired Compose) ([]byte, error) {
	normalized, err := CanonicalCompose(desired)
	if err != nil {
		return nil, err
	}
	payload, err := json.Marshal(normalized, digestOptions())
	if err != nil {
		return nil, fmt.Errorf("encode effective Compose: %w", err)
	}
	// TODO(0.16.0): Revisit this re-encoding when switching to the snapshot format
	// for the digest. It preserves the old object key order and number literals.
	var tree map[string]any
	if err = json.Unmarshal(payload, &tree, utils.PreserveJSONNumbers()); err != nil {
		return nil, fmt.Errorf("decode effective Compose: %w", err)
	}
	canonical, err := json.Marshal(tree, jsonv1.DefaultOptionsV1())
	if err != nil {
		return nil, fmt.Errorf("encode canonical Compose: %w", err)
	}
	return canonical, nil
}

// digestOptions encode Command as releases before typed normalization did, with
// an explicit form flag, because this encoding feeds persisted digests.
// TODO(0.16.0): Remove this override and use the snapshot command encoding.
func digestOptions() json.Options {
	type legacyCommand struct {
		Args   []string `json:"Args"`
		IsList bool     `json:"IsList"`
	}
	return json.JoinOptions(jsonv1.DefaultOptionsV1(), json.WithMarshalers(
		json.MarshalFunc(func(command Command) ([]byte, error) {
			return json.Marshal(legacyCommand{Args: command.Args, IsList: command.isList}, jsonv1.DefaultOptionsV1())
		}),
	))
}

// CanonicalCompose returns the normalized copy of desired whose encoding
// Canonicalize digests: equivalent syntax is normalized, references are
// resolved, and order-insensitive lists are sorted. Collections that
// normalization reorders or replaces are copied first, so desired is not modified.
func CanonicalCompose(desired Compose) (Compose, error) {
	desired.Services = slices.Clone(desired.Services)
	for i := range desired.Services {
		if err := canonicalizeService(&desired.Services[i], false); err != nil {
			return desired, err
		}
	}
	slices.SortStableFunc(desired.Services, func(left, right Service) int {
		return strings.Compare(left.Name, right.Name)
	})
	return desired, nil
}

func canonicalizeService(service *Service, preserveMountOrder bool) error {
	if err := service.normalizeCommands(); err != nil {
		return err
	}
	if service.Healthcheck != nil {
		health := service.Healthcheck.Canonical()
		service.Healthcheck = &health
	}
	service.CapAdd, service.CapDrop = NormalizeCapabilities(service.CapAdd, service.CapDrop)
	// Env files are already merged into the environment and can contain duplicate raw values.
	service.EnvFiles = nil
	service.Secrets = mapObjectRefs(service.Secrets, canonicalServiceSecret)
	service.Configs = mapObjectRefs(service.Configs, ObjectRef.ServiceConfig)
	service.Ports.Ports = slices.Clone(service.Ports.Ports)
	service.Networks = cloneNetworkList(service.Networks)
	// Init Jobs run in the specified order, so only their contents are normalized.
	service.InitJobs = slices.Clone(service.InitJobs)
	for i := range service.InitJobs {
		job := &service.InitJobs[i]
		if err := job.ValidateObjectRefs(); err != nil {
			return fmt.Errorf("services.%s.x-init-deploy-jobs[%d].%w", service.Name, i, err)
		}
		job.resolveObjectRefs()
		job.Networks = cloneNetworkList(job.Networks)
		err := sortByCanonicalJSON(job.Networks.list())
		if !preserveMountOrder {
			err = errors.Join(err, sortByCanonicalJSON(job.Secrets), sortByCanonicalJSON(job.Configs))
		}
		if err != nil {
			return fmt.Errorf("services.%s.init_jobs[%d]: %w", service.Name, i, err)
		}
	}
	// TODO(0.16.0): Drop the extra JSON sort for CapAdd and CapDrop. It preserves
	// the old digest order for escaped names. NormalizeCapabilities already sorts them.
	err := errors.Join(
		sortByCanonicalJSON(service.Ports.Ports),
		sortByCanonicalJSON(service.Networks.list()),
		sortByCanonicalJSON(service.CapAdd),
		sortByCanonicalJSON(service.CapDrop),
	)
	if !preserveMountOrder {
		service.Volumes.Volumes = slices.Clone(service.Volumes.Volumes)
		err = errors.Join(err, sortByCanonicalJSON(service.Volumes.Volumes),
			sortByCanonicalJSON(service.Secrets), sortByCanonicalJSON(service.Configs))
	}
	if err != nil {
		return fmt.Errorf("services.%s: %w", service.Name, err)
	}
	return nil
}

// HasOrderedMounts reports whether the service mounts two or more volumes, secrets
// or configs, including those of its Init Jobs. Their order is applied to Swarm,
// but CanonicalCompose sorts it out of the whole-stack digest.
func (s Service) HasOrderedMounts() bool {
	if len(s.Volumes.Volumes) > 1 || len(s.Secrets) > 1 || len(s.Configs) > 1 {
		return true
	}
	for _, job := range s.InitJobs {
		if len(job.Secrets) > 1 || len(job.Configs) > 1 {
			return true
		}
	}
	return false
}

// canonicalServiceSecret identifies a service secret by the path Docker mounts:
// a relative target is equivalent to its absolute form under /run/secrets/.
func canonicalServiceSecret(ref ObjectRef) ObjectRef {
	ref = ref.ServiceSecret()
	ref.Target = SecretPath(ref.Target)
	return ref
}

// cloneNetworkList copies networks and their attachment list, so the list can be sorted.
func cloneNetworkList(networks *ServiceNetworks) *ServiceNetworks {
	if networks == nil {
		return nil
	}
	clone := *networks
	clone.List = slices.Clone(networks.List)
	return &clone
}

// list returns the attachments of networks, which may be nil.
func (s *ServiceNetworks) list() []*ServiceNetwork {
	if s == nil {
		return nil
	}
	return s.List
}

// sortByCanonicalJSON stable-sorts values in place by the JSON encoding of its
// elements with object keys sorted, the order of persisted desired-state digests.
// Struct fields encode in declaration order, so plain encodings would order
// elements differently.
// TODO(0.16.0): Keep order-insensitive lists sorted, but stop reproducing the old
// JSON key order in their sort keys.
func sortByCanonicalJSON[T any](values []T) error {
	type keyed struct {
		key   []byte
		value T
	}
	entries := make([]keyed, len(values))
	for i, value := range values {
		key, err := canonicalJSON(value)
		if err != nil {
			return err
		}
		entries[i] = keyed{key: key, value: value}
	}
	slices.SortStableFunc(entries, func(left, right keyed) int {
		return bytes.Compare(left.key, right.key)
	})
	for i, entry := range entries {
		values[i] = entry.value
	}
	return nil
}

// canonicalJSON encodes value with object keys sorted and numbers in literal form.
func canonicalJSON(value any) ([]byte, error) {
	payload, err := json.Marshal(value, jsonv1.DefaultOptionsV1())
	if err != nil {
		return nil, fmt.Errorf("encode %T: %w", value, err)
	}
	var generic any
	if err = json.Unmarshal(payload, &generic, utils.PreserveJSONNumbers()); err != nil {
		return nil, fmt.Errorf("decode %T: %w", value, err)
	}
	return json.Marshal(generic, jsonv1.DefaultOptionsV1())
}
