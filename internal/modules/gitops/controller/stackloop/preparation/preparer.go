// Package preparation loads and prepares effective Compose definitions for deployment.
package preparation

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	pipe "github.com/artarts36/gopipe"
	"github.com/distribution/reference"
	downward "github.com/swarm-deploy/downward/go"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	gitx "github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/git"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/faults"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/fs"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/labelsdict"
	"go.opentelemetry.io/otel/trace"
)

const (
	renderedComposeDirectoryMode = 0o755
	renderedComposeFileMode      = 0o600
	imageViolationInvalid        = "image.invalid"
	imageViolationRequired       = "image.tag.required"
	imageViolationLatest         = "image.tag.no_latest"
	imageViolationOnlySHA        = "image.tag.only_sha"
)

// Image policy violation codes are preserved in preparation failures.
const (
	ImageViolationInvalid  = imageViolationInvalid
	ImageViolationRequired = imageViolationRequired
	ImageViolationLatest   = imageViolationLatest
	ImageViolationOnlySHA  = imageViolationOnlySHA
)

var downwardEnvironment = map[string]string{
	downward.EnvStackName:   "",
	downward.EnvServiceID:   "{{.Service.ID}}",
	downward.EnvServiceName: "{{.Service.Name}}",
	downward.EnvTaskID:      "{{.Task.ID}}",
	downward.EnvTaskName:    "{{.Task.Name}}",
	downward.EnvTaskSlot:    "{{.Task.Slot}}",
	downward.EnvNodeID:      "{{.Node.ID}}",
	downward.EnvNodeName:    "{{.Node.Hostname}}",
}

// Rotator rewrites config and secret names from their content.
type Rotator interface {
	// Rotate mutates config and secret names in the effective Compose definition.
	Rotate(ctx context.Context, file *compose.File, stackName string, hashLength int, includePath bool) (bool, error)
}

// Request contains the already loaded source definition and reconciliation context.
type Request struct {
	// Stack is the stack being prepared.
	Stack config.StackSpec
	// Desired is the source Compose definition.
	Desired *compose.File
	// SourceChanged reports whether the source digest differs from GitOps runtime state.
	SourceChanged bool
}

// Prepared is the effective Compose definition and its synchronous deployment path.
type Prepared struct {
	// Desired is the effective in-memory definition submitted to Deployments.
	Desired *compose.File
	// DeployComposePath is the source or rendered file passed to deployment execution.
	DeployComposePath string
	// RenderedComposePath is non-empty when a temporary effective definition was written.
	RenderedComposePath string
}

// Preparer owns deterministic Compose loading, mutation, rendering and temporary files.
type Preparer struct {
	cfg              *config.Config
	git              gitx.Repository
	fileSystem       fs.FileSystem
	composeLoader    compose.FileLoader
	envFilePopulator *compose.EnvFilePopulator
	rotator          Rotator
	pipeline         *pipe.Pipeline[*preparationPayload]
}

type preparationPayload struct {
	request  Request
	prepared *Prepared
	mutated  bool
}

// New creates a Compose preparer.
func New(cfg *config.Config, git gitx.Repository, fileSystem fs.FileSystem, rotator Rotator) *Preparer {
	preparer := &Preparer{cfg: cfg, git: git, fileSystem: fileSystem,
		composeLoader:    compose.NewFileLoaderWithReader(fileSystem.ReadFile),
		envFilePopulator: compose.NewEnvFilePopulator(), rotator: rotator}
	preparer.attachPipeline(nil)
	return preparer
}

// Load reads and validates the source Compose file without applying effective-state mutations.
func (p *Preparer) Load(ctx context.Context, stack config.StackSpec) (*compose.File, error) {
	return p.composeLoader.Load(ctx, filepath.Join(p.git.WorkingDir(), stack.ComposeFile))
}

// LoadFrom loads a stack from an immutable checkout and adjusts source references.
func (p *Preparer) LoadFrom(
	ctx context.Context, stack config.StackSpec, snapshot, original string,
) (*compose.File, error) {
	reader := func(ctx context.Context, path string) ([]byte, error) {
		resolved, err := snapshotSourcePath(path, snapshot, original)
		if err != nil {
			return nil, err
		}
		return p.fileSystem.ReadFile(ctx, resolved)
	}
	loader := compose.NewFileLoaderWithReader(reader)
	file, err := loader.Load(ctx, filepath.Join(snapshot, stack.ComposeFile))
	if err != nil {
		return nil, err
	}
	for _, object := range file.Compose.Configs {
		if object == nil || object.File == "" {
			continue
		}
		object.File, err = snapshotSourcePath(object.File, snapshot, original)
		if err != nil {
			return nil, err
		}
	}
	for _, object := range file.Compose.Secrets {
		if object == nil || object.File == "" {
			continue
		}
		object.File, err = snapshotSourcePath(object.File, snapshot, original)
		if err != nil {
			return nil, err
		}
	}
	return file, nil
}

func snapshotSourcePath(path, snapshot, original string) (string, error) {
	if !filepath.IsAbs(path) {
		return path, nil
	}
	for _, root := range []string{snapshot, original} {
		rel, err := filepath.Rel(root, path)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		return filepath.Join(snapshot, rel), nil
	}
	return "", fmt.Errorf("deployment input references file outside Git checkout: %s", filepath.Base(path))
}

// Prepare builds the effective definition and writes a rendered file when deployment requires one.
func (p *Preparer) Prepare(ctx context.Context, req Request) (*Prepared, error) {
	prepared := &Prepared{Desired: req.Desired, DeployComposePath: req.Desired.Path}
	payload := &preparationPayload{request: req, prepared: prepared}
	if err := p.pipeline.Run(ctx, payload); err != nil {
		p.Cleanup(ctx, prepared)
		return prepared, err
	}
	return prepared, nil
}

// Keep preparation in its own pipeline so stage errors, panic isolation, metrics,
// and step spans retain the behavior they had before extraction from StackReconciler.
func (p *Preparer) attachPipeline(provider trace.TracerProvider) {
	p.pipeline = pipe.NewPipelineWithConfig[*preparationPayload](pipe.Config{
		PipelineName:   "prepare stack",
		Logger:         faults.NewSafeLogger(slog.Default()),
		TracerProvider: provider,
	})

	if p.cfg.Spec.Sync.Policy.Image.Enabled() {
		p.pipeline.Add(pipe.Step[*preparationPayload]{
			Name: "check image policy",
			Run: func(_ context.Context, payload *preparationPayload) error {
				return checkImagePolicy(p.cfg.Spec.Sync.Policy.Image, payload.prepared.Desired)
			},
		})
	}

	p.pipeline.Add(pipe.Step[*preparationPayload]{
		Name: "populate environment",
		When: pipe.When(func(payload *preparationPayload) bool {
			for _, service := range payload.prepared.Desired.Compose.Services {
				if len(service.EnvFiles) > 0 {
					return true
				}
			}
			return false
		}),
		Run: func(_ context.Context, payload *preparationPayload) error {
			payload.mutated = p.envFilePopulator.Populate(payload.prepared.Desired) || payload.mutated
			return nil
		},
	})

	if p.cfg.Spec.Containers.Downward != nil {
		p.pipeline.Add(pipe.Step[*preparationPayload]{
			Name: "add downward",
			Run: func(_ context.Context, payload *preparationPayload) error {
				changed := addDownward(payload.prepared.Desired, payload.request.Stack.Name)
				if changed && payload.request.SourceChanged {
					payload.mutated = true
				}
				return nil
			},
		})
	}

	p.pipeline.Add(pipe.Step[*preparationPayload]{
		Name: "add managed label",
		Run: func(_ context.Context, payload *preparationPayload) error {
			payload.mutated = addManagedLabel(payload.prepared.Desired) || payload.mutated
			return nil
		},
	})

	if p.cfg.Spec.SecretRotation.Enabled {
		p.pipeline.Add(pipe.Step[*preparationPayload]{
			Name: "rotate secrets/configs",
			Run: func(ctx context.Context, payload *preparationPayload) error {
				changed, err := p.rotator.Rotate(ctx, payload.prepared.Desired, payload.request.Stack.Name,
					p.cfg.Spec.SecretRotation.HashLength, p.cfg.Spec.SecretRotation.IncludePath)
				payload.mutated = payload.mutated || changed
				return err
			},
		})
	}

	p.pipeline.Add(pipe.Step[*preparationPayload]{
		Name: "write rendered compose",
		When: pipe.When(func(payload *preparationPayload) bool {
			return payload.mutated || needsRenderedCompose(payload.prepared.Desired)
		}),
		Run: func(ctx context.Context, payload *preparationPayload) error {
			return p.writeRenderedCompose(ctx, payload.request.Stack.Name, payload.prepared)
		},
	})
}

// Cleanup removes a rendered Compose file after synchronous deployment no longer needs it.
func (p *Preparer) Cleanup(ctx context.Context, prepared *Prepared) {
	if prepared == nil || prepared.RenderedComposePath == "" {
		return
	}
	if err := p.fileSystem.RemoveFile(ctx, prepared.RenderedComposePath); err != nil && !errors.Is(err, os.ErrNotExist) {
		slog.WarnContext(ctx, "failed to clean up rendered compose file",
			"path", prepared.RenderedComposePath, "err", err)
	}
}

// CleanupStale removes rendered files left by a previous controller after ownership handoff.
func (p *Preparer) CleanupStale(ctx context.Context) error {
	return os.RemoveAll(filepath.Join(p.cfg.Spec.DataDir, "rendered"))
}

// DeploymentCompose returns the normalized effective Compose definition used for service-level self-healing.
func (p *Preparer) DeploymentCompose(desired *compose.File, stackName string) compose.Compose {
	rendered := stackDeployCompose(desired, stackName)
	p.normalizeRenderedObjectFilePaths(rendered)
	return rendered.Compose
}

func (p *Preparer) writeRenderedCompose(ctx context.Context, stackName string, prepared *Prepared) error {
	renderedDir := filepath.Join(p.cfg.Spec.DataDir, "rendered")
	if err := p.fileSystem.CreateDirectory(ctx, renderedDir, renderedComposeDirectoryMode); err != nil {
		return fmt.Errorf("create rendered dir: %w", err)
	}
	rendered := stackDeployCompose(prepared.Desired, stackName)
	p.normalizeRenderedObjectFilePaths(rendered)
	content, err := rendered.MarshalYAML()
	if err != nil {
		return fmt.Errorf("failed to marshal desired compose yaml: %w", err)
	}
	target := filepath.Join(renderedDir, stackName+".yaml")
	prepared.RenderedComposePath = target
	if err = p.fileSystem.WriteFile(ctx, target, content, renderedComposeFileMode); err != nil {
		return fmt.Errorf("write rendered compose %s: %w", target, err)
	}
	prepared.DeployComposePath = target
	return nil
}

func checkImagePolicy(policy config.ImagePolicySpec, desired *compose.File) error {
	issues := make([]compose.ValidationIssue, 0)
	for _, service := range desired.Compose.Services {
		if code := imagePolicyViolation(policy, service.Image); code != "" {
			issues = append(issues, compose.ValidationIssue{ResourceType: "service", ResourceName: service.Name,
				Field: "image", Code: code, Message: imagePolicyViolationMessage(code, service.Image)})
		}
		for _, job := range service.InitJobs {
			if code := imagePolicyViolation(policy, job.Image); code != "" {
				issues = append(issues, compose.ValidationIssue{ResourceType: "service", ResourceName: service.Name,
					Field: fmt.Sprintf("x-init-deploy-jobs[%q].image", job.Name), Code: code,
					Message: fmt.Sprintf("init job %q: %s", job.Name, imagePolicyViolationMessage(code, job.Image))})
			}
		}
	}
	if len(issues) > 0 {
		return &compose.ValidateComposeError{FilePath: desired.Path, Issues: issues}
	}
	return nil
}

// ValidateImagePolicy validates service and init-job images in a source Compose definition.
func ValidateImagePolicy(policy config.ImagePolicySpec, desired *compose.File) error {
	return checkImagePolicy(policy, desired)
}

func imagePolicyViolation(policy config.ImagePolicySpec, image string) string {
	if !policy.Enabled() {
		return ""
	}
	named, err := reference.ParseNormalizedNamed(strings.TrimSpace(image))
	if err != nil {
		return imageViolationInvalid
	}
	_, hasDigest := named.(reference.Canonical)
	tagged, hasTag := named.(reference.NamedTagged)
	tag := ""
	if hasTag {
		tag = tagged.Tag()
	}
	switch {
	case policy.Tag.OnlySHA && !hasDigest:
		return imageViolationOnlySHA
	case policy.Tag.Required && !hasTag && !hasDigest:
		return imageViolationRequired
	case policy.Tag.NoLatest && ((hasTag && tag == "latest") || (!hasTag && !hasDigest)):
		return imageViolationLatest
	default:
		return ""
	}
}

// ImagePolicyViolation returns the stable validation code for an image reference.
func ImagePolicyViolation(policy config.ImagePolicySpec, image string) string {
	return imagePolicyViolation(policy, image)
}

func imagePolicyViolationMessage(code, image string) string {
	switch code {
	case imageViolationInvalid:
		return fmt.Sprintf("image reference %q is invalid", image)
	case imageViolationRequired:
		return fmt.Sprintf("image %q must have an explicit tag or digest", image)
	case imageViolationLatest:
		return fmt.Sprintf("image %q must not use the latest tag", image)
	case imageViolationOnlySHA:
		return fmt.Sprintf("image %q must use a digest", image)
	default:
		return "image policy rejected the service image"
	}
}

func addDownward(desired *compose.File, stackName string) bool {
	changed := false
	for index, service := range desired.Compose.Services {
		if service.Environment.Map == nil {
			service.Environment.Map = make(map[string]string, len(downwardEnvironment))
		}
		serviceChanged := false
		for key, value := range downwardEnvironment {
			if key == downward.EnvStackName {
				value = stackName
			}
			if service.Environment.Has(key) {
				continue
			}
			service.Environment.Map[key] = value
			serviceChanged = true
		}
		if serviceChanged {
			desired.Compose.Services[index] = service
			changed = true
		}
	}
	return changed
}

func addManagedLabel(desired *compose.File) bool {
	changed := false
	for index, service := range desired.Compose.Services {
		if service.Deploy.Labels.Add(labelsdict.ServiceManagedLabelKey, labelsdict.ServiceManagedLabelValue) {
			desired.Compose.Services[index] = service
			changed = true
		}
	}
	return changed
}

func needsRenderedCompose(desired *compose.File) bool {
	for _, secret := range desired.Compose.Secrets {
		if !secret.External {
			return true
		}
	}
	for _, service := range desired.Compose.Services {
		if len(service.InitJobs) > 0 {
			return true
		}
	}
	return false
}

func stackDeployCompose(desired *compose.File, stackName string) *compose.File {
	rendered := *desired
	rendered.Compose = desired.Compose
	rendered.Compose.Services = append(compose.Services(nil), desired.Compose.Services...)
	rendered.Compose.Configs = cloneObjects(desired.Compose.Configs)
	rendered.Compose.Secrets = cloneObjects(desired.Compose.Secrets)
	for alias, secret := range rendered.Compose.Secrets {
		if secret.External {
			continue
		}
		name := secret.Name
		if name == "" {
			name = stackName + "_" + alias
		}
		rendered.Compose.Secrets[alias] = &compose.Secret{Name: name, External: true}
	}
	for index := range rendered.Compose.Services {
		rendered.Compose.Services[index].InitJobs = nil
	}
	return &rendered
}

func cloneObjects[T any, Objects ~map[string]*T](objects Objects) Objects {
	if objects == nil {
		return nil
	}
	cloned := make(Objects, len(objects))
	for alias, object := range objects {
		if object == nil {
			cloned[alias] = nil
			continue
		}
		objectCopy := *object
		cloned[alias] = &objectCopy
	}
	return cloned
}

func (p *Preparer) normalizeRenderedObjectFilePaths(file *compose.File) {
	baseDir := filepath.Dir(file.Path)
	normalizeConfigFilePaths(baseDir, file.Compose.Configs)
	normalizeSecretFilePaths(baseDir, file.Compose.Secrets)
	for index, service := range file.Compose.Services {
		file.Compose.Services[index].EnvFiles = normalizeEnvFiles(p.git.WorkingDir(), baseDir, service.EnvFiles)
	}
}

func normalizeEnvFiles(repoDir, baseDir string, envFiles compose.EnvFiles) compose.EnvFiles {
	result := make(compose.EnvFiles, len(envFiles))
	for index, file := range envFiles {
		result[index] = file
		if filepath.IsAbs(file.Path) {
			result[index].Path = filepath.Join(repoDir, file.Path)
		} else {
			result[index].Path = filepath.Join(baseDir, file.Path)
		}
	}
	return result
}

func normalizeConfigFilePaths(baseDir string, objects compose.Configs) {
	for _, object := range objects {
		if !object.External && object.File != "" && !filepath.IsAbs(object.File) {
			object.File = filepath.Clean(filepath.Join(baseDir, object.File))
		}
	}
}

func normalizeSecretFilePaths(baseDir string, objects compose.Secrets) {
	for _, object := range objects {
		if !object.External && object.File != "" && !filepath.IsAbs(object.File) {
			object.File = filepath.Clean(filepath.Join(baseDir, object.File))
		}
	}
}
