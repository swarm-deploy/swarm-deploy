// Package preparation loads and prepares effective Compose definitions for deployment.
package preparation

import (
	"context"
	"log/slog"
	"path/filepath"

	pipe "github.com/artarts36/gopipe"
	downward "github.com/swarm-deploy/downward/go"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/controller/stackloop/policies"
	gitx "github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/git"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/faults"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/labelsdict"
	"go.opentelemetry.io/otel/trace"
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
	Rotate(file *compose.File, stackName string, hashLength int, includePath bool) (bool, error)
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
	envFilePopulator *compose.EnvFilePopulator
	rotator          Rotator
	pipeline         *pipe.Pipeline[*preparationPayload]
}

type preparationPayload struct {
	request Request
	desired *compose.File
}

// New creates a Compose preparer.
func New(cfg *config.Config, git gitx.Repository, rotator Rotator) *Preparer {
	preparer := &Preparer{cfg: cfg, git: git,
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
	// Preserve the checkout location for resolving relative bind mounts.
	file.Path = filepath.Join(p.git.WorkingDir(), stack.ComposeFile)
	return file, nil
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

	if p.cfg.Spec.GitOps.Sync.Policy.Image.Enabled() {
		p.pipeline.Add(pipe.Step[*preparationPayload]{
			Name: "check image policy",
			Run: func(ctx context.Context, payload *preparationPayload) error {
				return checkImagePolicy(ctx, p.cfg.Spec.GitOps.Sync.Policy.Image, payload.desired)
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

	if p.cfg.Spec.GitOps.Injection.Downward.Enabled {
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

	if p.cfg.Spec.GitOps.ResourceRotation.Enabled {
		p.pipeline.Add(pipe.Step[*preparationPayload]{
			Name: "rotate secrets/configs",
			Run: func(_ context.Context, payload *preparationPayload) error {
				_, err := p.rotator.Rotate(payload.desired, payload.request.Stack.Name,
					p.cfg.Spec.GitOps.ResourceRotation.HashLength, p.cfg.Spec.GitOps.ResourceRotation.IncludePath)
				return err
			},
		})
	}

	p.pipeline.Add(pipe.Step[*preparationPayload]{
		Name: "validate desired state",
		Run: func(_ context.Context, payload *preparationPayload) error {
			return compose.ValidateEffective(payload.desired.Compose, payload.desired.Path)
		},
	})
}

func checkImagePolicy(ctx context.Context, policy config.ImagePolicySpec, desired *compose.File) error {
	checker := policies.NewImageChecker(policy)
	issues := make([]compose.ValidationIssue, 0)
	for _, service := range desired.Compose.Services {
		issues = append(issues, checker.Check(ctx, service)...)
	}
	if len(issues) > 0 {
		return &compose.ValidateComposeError{FilePath: desired.Path, Issues: issues}
	}
	return nil
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
