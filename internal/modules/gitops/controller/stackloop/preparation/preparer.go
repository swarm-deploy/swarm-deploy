// Package preparation loads and prepares effective Compose definitions for deployment.
package preparation

import (
	"context"
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
	imageViolationInvalid  = "image.invalid"
	imageViolationRequired = "image.tag.required"
	imageViolationLatest   = "image.tag.no_latest"
	imageViolationOnlySHA  = "image.tag.only_sha"
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
}

// Preparer owns Compose loading and deterministic effective-state preparation.
type Preparer struct {
	cfg              *config.Config
	git              gitx.Repository
	composeLoader    compose.FileLoader
	envFilePopulator *compose.EnvFilePopulator
	rotator          Rotator
	pipeline         *pipe.Pipeline[*preparationPayload]
}

type preparationPayload struct {
	request Request
	desired *compose.File
}

// New creates a Compose preparer.
func New(cfg *config.Config, git gitx.Repository, fileSystem fs.FileSystem, rotator Rotator) *Preparer {
	preparer := &Preparer{cfg: cfg, git: git,
		composeLoader:    compose.NewFileLoaderWithReader(fileSystem.ReadFile),
		envFilePopulator: compose.NewEnvFilePopulator(), rotator: rotator}
	preparer.attachPipeline(nil)
	return preparer
}

// Load reads and validates the source Compose file without applying effective-state mutations.
func (p *Preparer) Load(ctx context.Context, stack config.StackSpec) (*compose.File, error) {
	return p.composeLoader.Load(ctx, filepath.Join(p.git.WorkingDir(), stack.ComposeFile))
}

// Prepare builds the effective definition used directly by native deployment.
func (p *Preparer) Prepare(ctx context.Context, req Request) (*compose.File, error) {
	payload := &preparationPayload{request: req, desired: req.Desired}
	if err := p.pipeline.Run(ctx, payload); err != nil {
		return req.Desired, err
	}
	return req.Desired, nil
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
				return checkImagePolicy(p.cfg.Spec.Sync.Policy.Image, payload.desired)
			},
		})
	}

	p.pipeline.Add(pipe.Step[*preparationPayload]{
		Name: "populate environment",
		When: pipe.When(func(payload *preparationPayload) bool {
			for _, service := range payload.desired.Compose.Services {
				if len(service.EnvFiles) > 0 {
					return true
				}
			}
			return false
		}),
		Run: func(_ context.Context, payload *preparationPayload) error {
			p.envFilePopulator.Populate(payload.desired)
			return nil
		},
	})

	p.pipeline.Add(pipe.Step[*preparationPayload]{
		Name: "prepare bind mount paths",
		Run: func(_ context.Context, payload *preparationPayload) error {
			normalizeBindMountPaths(filepath.Dir(payload.desired.Path), payload.desired.Compose.Services)
			return nil
		},
	})

	if p.cfg.Spec.Containers.Downward != nil {
		p.pipeline.Add(pipe.Step[*preparationPayload]{
			Name: "add downward",
			Run: func(_ context.Context, payload *preparationPayload) error {
				addDownward(payload.desired, payload.request.Stack.Name)
				return nil
			},
		})
	}

	p.pipeline.Add(pipe.Step[*preparationPayload]{
		Name: "add managed label",
		Run: func(_ context.Context, payload *preparationPayload) error {
			addManagedLabel(payload.desired)
			return nil
		},
	})

	if p.cfg.Spec.SecretRotation.Enabled {
		p.pipeline.Add(pipe.Step[*preparationPayload]{
			Name: "rotate secrets/configs",
			Run: func(ctx context.Context, payload *preparationPayload) error {
				_, err := p.rotator.Rotate(ctx, payload.desired, payload.request.Stack.Name,
					p.cfg.Spec.SecretRotation.HashLength, p.cfg.Spec.SecretRotation.IncludePath)
				return err
			},
		})
	}
}

// CleanupStale removes legacy CLI artifacts after controller ownership handoff.
func (p *Preparer) CleanupStale(ctx context.Context) error {
	return os.RemoveAll(filepath.Join(p.cfg.Spec.DataDir, "rendered"))
}

func checkImagePolicy(policy config.ImagePolicySpec, desired *compose.File) error {
	issues := make([]compose.ValidationIssue, 0)
	for _, service := range desired.Compose.Services {
		if code := imagePolicyViolation(policy, service.Image); code != "" {
			issues = append(issues, compose.ValidationIssue{ResourceType: "service", ResourceName: service.Name,
				Field: "image", Code: code, Message: imagePolicyViolationMessage(code)})
		}
		for _, job := range service.InitJobs {
			if code := imagePolicyViolation(policy, job.Image); code != "" {
				issues = append(issues, compose.ValidationIssue{ResourceType: "service", ResourceName: service.Name,
					Field: fmt.Sprintf("x-init-deploy-jobs[%q].image", job.Name), Code: code,
					Message: fmt.Sprintf("init job %q: %s", job.Name, imagePolicyViolationMessage(code))})
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

func imagePolicyViolationMessage(code string) string {
	switch code {
	case imageViolationInvalid:
		return "image reference is invalid"
	case imageViolationRequired:
		return "image must have an explicit tag or digest"
	case imageViolationLatest:
		return "image must not use the latest tag"
	case imageViolationOnlySHA:
		return "image must use a digest"
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

func normalizeBindMountPaths(baseDir string, services compose.Services) {
	for serviceIndex := range services {
		for _, volume := range services[serviceIndex].Volumes.Volumes {
			if volume == nil || volume.Type != compose.ServiceVolumeTypeBind || volume.Source == "" ||
				filepath.IsAbs(volume.Source) {
				continue
			}
			volume.Source = filepath.Clean(filepath.Join(baseDir, volume.Source))
		}
	}
}
