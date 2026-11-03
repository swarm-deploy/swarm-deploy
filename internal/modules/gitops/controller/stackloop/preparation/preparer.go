// Package preparation loads and prepares effective Compose definitions for deployment.
package preparation

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
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
	renderedComposeDirectoryMode = 0o700
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
	// DeployComposePath is the rendered file passed to deployment execution.
	DeployComposePath string
	// DeployCompose is the definition reused by single-service self-healing.
	DeployCompose   compose.Compose
	configDirectory string
}

// Preparer owns deterministic Compose loading, mutation, rendering and temporary files.
type Preparer struct {
	cfg              *config.Config
	git              gitx.Repository
	fileSystem       fs.FileSystem
	envFilePopulator *compose.EnvFilePopulator
	rotator          Rotator
	pipeline         *pipe.Pipeline[*preparationPayload]
}

type preparationPayload struct {
	request  Request
	prepared *Prepared
	// mutated tracks the original render conditions, which determine the base for relative bind mounts.
	mutated bool
}

// New creates a Compose preparer.
func New(cfg *config.Config, git gitx.Repository, fileSystem fs.FileSystem, rotator Rotator) *Preparer {
	preparer := &Preparer{cfg: cfg, git: git, fileSystem: fileSystem,
		envFilePopulator: compose.NewEnvFilePopulator(), rotator: rotator}
	preparer.attachPipeline(nil)
	return preparer
}

// Load reads and validates a Compose file and its referenced sources at one commit.
func (p *Preparer) Load(ctx context.Context, stack config.StackSpec, revision string) (*compose.File, error) {
	loader := compose.NewFileLoaderWithReader(func(ctx context.Context, path string) ([]byte, error) {
		return p.git.ReadFile(ctx, revision, path)
	})
	file, err := loader.Load(ctx, stack.ComposeFile)
	if err != nil {
		return nil, err
	}
	// Keep the source location as metadata. Deployment never opens this path.
	file.Path = filepath.Join(p.git.WorkingDir(), stack.ComposeFile)
	return file, nil
}

// LoadCurrent loads the current revision for standalone resource cleanup.
func (p *Preparer) LoadCurrent(ctx context.Context, stack config.StackSpec) (*compose.File, error) {
	revision, err := p.git.Head(ctx)
	if err != nil {
		return nil, fmt.Errorf("resolve repository head: %w", err)
	}
	return p.Load(ctx, stack, revision)
}

// Prepare builds the effective definition and writes the rendered file used for deployment.
func (p *Preparer) Prepare(ctx context.Context, req Request) (*Prepared, error) {
	prepared := &Prepared{Desired: req.Desired}
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
				payload.mutated = (changed && payload.request.SourceChanged) || payload.mutated
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
		Run: func(ctx context.Context, payload *preparationPayload) error {
			return p.writeRenderedCompose(ctx, payload)
		},
	})
}

// Cleanup removes the rendered Compose and config files after synchronous deployment.
func (p *Preparer) Cleanup(ctx context.Context, prepared *Prepared) {
	if prepared == nil {
		return
	}
	if prepared.DeployComposePath != "" {
		if err := p.fileSystem.RemoveFile(ctx, prepared.DeployComposePath); err != nil && !errors.Is(err, os.ErrNotExist) {
			slog.WarnContext(ctx, "failed to clean up rendered compose file",
				"path", prepared.DeployComposePath, "err", err)
		}
	}
	if prepared.configDirectory != "" {
		if err := os.RemoveAll(prepared.configDirectory); err != nil {
			slog.WarnContext(ctx, "failed to clean up temporary configs", "path", prepared.configDirectory, "err", err)
		}
	}
}

// CleanupStale removes rendered files left by a previous controller after ownership handoff.
func (p *Preparer) CleanupStale(ctx context.Context) error {
	return os.RemoveAll(filepath.Join(p.cfg.Spec.DataDir, "rendered"))
}

func (p *Preparer) writeRenderedCompose(ctx context.Context, payload *preparationPayload) error {
	stackName := payload.request.Stack.Name
	prepared := payload.prepared
	if !filepath.IsLocal(stackName) || filepath.Base(stackName) != stackName || strings.HasPrefix(stackName, "-") {
		return fmt.Errorf("invalid stack name %q for deployment", stackName)
	}
	renderedDir := filepath.Join(p.cfg.Spec.DataDir, "rendered")
	if err := p.fileSystem.CreateDirectory(ctx, renderedDir, renderedComposeDirectoryMode); err != nil {
		return fmt.Errorf("create rendered dir: %w", err)
	}
	rendered := stackDeployCompose(prepared.Desired, stackName)
	if err := materializeConfigs(renderedDir, rendered.Compose.Configs, prepared); err != nil {
		return err
	}
	prepared.DeployCompose = rendered.Compose
	if !payload.mutated && !needsRenderedObjects(prepared.Desired.Compose) {
		preserveRelativeBindPaths(rendered)
	}
	content, err := rendered.MarshalYAML()
	if err != nil {
		return fmt.Errorf("failed to marshal desired compose yaml: %w", err)
	}
	target := filepath.Join(renderedDir, stackName+".yaml")
	prepared.DeployComposePath = target
	if err = p.fileSystem.WriteFile(ctx, target, content, renderedComposeFileMode); err != nil {
		return fmt.Errorf("write rendered compose %s: %w", target, err)
	}
	return nil
}

// needsRenderedObjects mirrors the previous render requirements apart from mutations.
func needsRenderedObjects(desired compose.Compose) bool {
	for _, secret := range desired.Secrets {
		if !secret.External {
			return true
		}
	}
	for _, service := range desired.Services {
		if len(service.InitJobs) > 0 {
			return true
		}
	}
	return false
}

// Stacks that used the source manifest retain its base for relative bind mounts.
func preserveRelativeBindPaths(file *compose.File) {
	base := strings.ReplaceAll(filepath.Dir(file.Path), "$", "$$")
	file.Compose.Services = slices.Clone(file.Compose.Services)
	for index := range file.Compose.Services {
		service := &file.Compose.Services[index]
		service.Volumes.Volumes = slices.Clone(service.Volumes.Volumes)
		for i, volume := range service.Volumes.Volumes {
			if volume.Type != compose.ServiceVolumeTypeBind || filepath.IsAbs(volume.Source) ||
				volume.Source == "" || strings.HasPrefix(volume.Source, "~") {
				continue
			}
			volumeCopy := *volume
			volumeCopy.Source = filepath.Join(base, volume.Source)
			service.Volumes.Volumes[i] = &volumeCopy
		}
	}
}

// Docker CLI still applies the configs. Give it files containing only the already loaded bytes.
func materializeConfigs(renderedDir string, configs compose.Configs, prepared *Prepared) error {
	for alias, config := range configs {
		if config.External {
			continue
		}
		if config.Data == nil {
			return fmt.Errorf("config %q has no loaded file", alias)
		}
		if prepared.configDirectory == "" {
			root, err := filepath.Abs(renderedDir)
			if err != nil {
				return fmt.Errorf("resolve rendered directory: %w", err)
			}
			prepared.configDirectory, err = os.MkdirTemp(root, "configs-")
			if err != nil {
				return fmt.Errorf("create temporary config directory: %w", err)
			}
		}
		file, err := os.CreateTemp(prepared.configDirectory, "config-")
		if err != nil {
			return fmt.Errorf("create temporary file for config %q: %w", alias, err)
		}
		_, writeErr := file.Write(config.Data)
		if fileErr := errors.Join(writeErr, file.Close()); fileErr != nil {
			return fmt.Errorf("write temporary file for config %q: %w", alias, fileErr)
		}
		config.File = strings.ReplaceAll(file.Name(), "$", "$$")
		config.Data = nil
	}
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

func stackDeployCompose(desired *compose.File, stackName string) *compose.File {
	rendered := *desired
	rendered.Compose = desired.Compose
	rendered.Compose.Services = append(compose.Services(nil), desired.Compose.Services...)
	rendered.Compose.Configs = make(compose.Configs, len(desired.Compose.Configs))
	for alias, config := range desired.Compose.Configs {
		configCopy := *config
		// Only materializeConfigs can set a path in the rendered definition.
		configCopy.File = ""
		rendered.Compose.Configs[alias] = &configCopy
	}
	rendered.Compose.Secrets = make(compose.Secrets, len(desired.Compose.Secrets))
	for alias, secret := range desired.Compose.Secrets {
		rendered.Compose.Secrets[alias] = &compose.Secret{
			Name: deployResourceName(stackName, alias, secret.Name, secret.External), External: true,
		}
	}
	for index := range rendered.Compose.Services {
		service := &rendered.Compose.Services[index]
		service.InitJobs = nil
		service.EnvFiles = nil
		service.Environment = service.Environment.ForStackDeploy()
	}
	return &rendered
}

func deployResourceName(stackName, alias, name string, external bool) string {
	if name != "" {
		return name
	}
	if external {
		return alias
	}
	return stackName + "_" + alias
}
