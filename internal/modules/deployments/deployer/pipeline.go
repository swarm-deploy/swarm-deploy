package deployer

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"

	pipe "github.com/artarts36/gopipe"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer/initjob"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer/secrets"
	stackpkg "github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer/stack"
	waiterpkg "github.com/swarm-deploy/swarm-deploy/internal/modules/deployments/deployer/waiter"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/faults"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/tracing"
	"go.opentelemetry.io/otel/attribute"
)

const (
	stepReconcileResources = "reconcile resources"
	stepRunInitJobs        = "run init jobs"
	stepCaptureBaseline    = "capture service baseline"
	stepApplyStack         = "apply stack"
	stepApplyService       = "apply service"
	stepWaitConvergence    = "wait for service convergence"
	stepPrepareService     = "prepare single-service compose"
)

func (d *Deployer) attachPipelines() {
	d.pipelineOnce.Do(func() {
		if d.stackApplier == nil {
			d.stackApplier = stackpkg.NewApplier(d.runner, d.stackDeployArgs)
		}
		d.deployStackPipe = d.newDeployStackPipeline()
		d.deployServicePipe = d.newDeployServicePipeline()
	})
}

func (d *Deployer) newDeployStackPipeline() *pipe.Pipeline[*deployStackPayload] {
	pipeline := pipe.NewPipelineWithConfig[*deployStackPayload](pipe.Config{
		PipelineName: "deployer.DeployStack",
		Logger:       faults.NewSafeLogger(slog.Default()),
	})
	attrs := pipe.Attributes[*deployStackPayload]{Trace: func(payload *deployStackPayload) []attribute.KeyValue {
		return []attribute.KeyValue{
			tracing.ResourceStackName.String(payload.stackName),
			tracing.ResourceComposePath.String(payload.deployComposePath),
		}
	}}
	pipeline.Add(pipe.Step[*deployStackPayload]{
		Name: stepReconcileResources, Attributes: attrs, Run: d.reconcileStackResources,
	})
	pipeline.Add(pipe.Step[*deployStackPayload]{Name: stepRunInitJobs, Attributes: attrs, Run: d.runStackInitJobs})
	pipeline.Add(pipe.Step[*deployStackPayload]{Name: stepCaptureBaseline, Attributes: attrs, Run: d.captureStackBaseline})
	pipeline.Add(pipe.Step[*deployStackPayload]{Name: stepApplyStack, Attributes: attrs, Run: d.applyStack})
	pipeline.Add(pipe.Step[*deployStackPayload]{Name: stepWaitConvergence, Attributes: attrs, Run: d.waitForStack})
	return pipeline
}

func (d *Deployer) newDeployServicePipeline() *pipe.Pipeline[*deployServicePayload] {
	pipeline := pipe.NewPipelineWithConfig[*deployServicePayload](pipe.Config{
		PipelineName: "deployer.DeployService",
		Logger:       faults.NewSafeLogger(slog.Default()),
	})
	attrs := pipe.Attributes[*deployServicePayload]{Trace: func(payload *deployServicePayload) []attribute.KeyValue {
		return []attribute.KeyValue{
			tracing.ResourceStackName.String(payload.stackName),
			tracing.ResourceServiceName.String(payload.serviceName),
		}
	}}
	pipeline.Add(pipe.Step[*deployServicePayload]{Name: stepPrepareService, Attributes: attrs, Run: d.prepareService})
	pipeline.Add(pipe.Step[*deployServicePayload]{
		Name: stepReconcileResources, Attributes: attrs, Run: d.reconcileServiceResources,
	})
	pipeline.Add(pipe.Step[*deployServicePayload]{
		Name: stepCaptureBaseline, Attributes: attrs, Run: d.captureServiceBaseline,
	})
	pipeline.Add(pipe.Step[*deployServicePayload]{Name: stepApplyService, Attributes: attrs, Run: d.applyService})
	pipeline.Add(pipe.Step[*deployServicePayload]{Name: stepWaitConvergence, Attributes: attrs, Run: d.waitForService})
	return pipeline
}

func (d *Deployer) reconcileStackResources(ctx context.Context, payload *deployStackPayload) error {
	resolved, err := d.reconcileResources(ctx, payload.stackName, payload.sourceComposePath, payload.desired)
	payload.resolved = resolved
	return safePipelineError(err)
}

func (d *Deployer) runStackInitJobs(ctx context.Context, payload *deployStackPayload) error {
	results, err := d.runInitJobs(ctx, payload.stackName, payload.desired.Services, payload.resolved)
	payload.result.InitJobs = results
	return safePipelineError(err)
}

func (d *Deployer) captureStackBaseline(ctx context.Context, payload *deployStackPayload) error {
	return safePipelineError(d.captureBaseline(ctx, payload.stackName, &payload.applyPayload))
}

func (d *Deployer) applyStack(_ context.Context, payload *deployStackPayload) error {
	return safePipelineError(d.stackApplier.Apply(payload.ctx, payload.stackName, payload.deployComposePath))
}

func (d *Deployer) waitForStack(_ context.Context, payload *deployStackPayload) error {
	serviceNames := make([]string, 0, len(payload.desired.Services))
	for _, service := range payload.desired.Services {
		serviceNames = append(serviceNames, service.Name)
	}
	return safePipelineError(d.wait(payload.ctx, waiterpkg.ServiceWaitRequest{
		StackName: payload.stackName, ServiceNames: serviceNames, Baseline: payload.baseline, OnlyAffected: true,
	}))
}

func (d *Deployer) prepareService(_ context.Context, payload *deployServicePayload) error {
	prepared, err := stackpkg.PrepareService(payload.desired, payload.deploy, payload.serviceName)
	payload.prepared = prepared
	return safePipelineError(err)
}

func (d *Deployer) reconcileServiceResources(ctx context.Context, payload *deployServicePayload) error {
	resolved, err := d.reconcileResources(ctx, payload.stackName, payload.sourceComposePath, payload.prepared.Desired)
	payload.resolved = resolved
	return safePipelineError(err)
}

func (d *Deployer) captureServiceBaseline(ctx context.Context, payload *deployServicePayload) error {
	return safePipelineError(d.captureBaseline(ctx, payload.stackName, &payload.applyPayload))
}

func (d *Deployer) applyService(_ context.Context, payload *deployServicePayload) error {
	return safePipelineError(d.stackApplier.Apply(payload.ctx, payload.stackName, payload.prepared.Path))
}

func (d *Deployer) waitForService(_ context.Context, payload *deployServicePayload) error {
	return safePipelineError(d.wait(payload.ctx, waiterpkg.ServiceWaitRequest{
		StackName: payload.stackName, ServiceNames: []string{payload.serviceName}, Baseline: payload.baseline,
		OnlyAffected: false,
	}))
}

type pipelineError struct {
	cause error
}

var pipelineCredential = regexp.MustCompile(`(?i)\b(password|passwd|secret|token|credential|api[_-]?key)=([^\s]+)`)

func (e pipelineError) Error() string {
	return pipelineCredential.ReplaceAllString(e.cause.Error(), `${1}=****`)
}
func (e pipelineError) Unwrap() error { return e.cause }

func safePipelineError(err error) error {
	if err == nil {
		return nil
	}
	return pipelineError{cause: err}
}

func (d *Deployer) captureBaseline(ctx context.Context, stackName string, payload *applyPayload) error {
	payload.ctx, payload.cancel = context.WithTimeout(ctx, binaryTimeout)
	baseline, err := d.serviceWaiter.Snapshot(payload.ctx, stackName)
	payload.baseline = baseline
	return err
}

func (d *Deployer) wait(ctx context.Context, request waiterpkg.ServiceWaitRequest) error {
	if err := d.serviceWaiter.Wait(ctx, request); err != nil {
		return fmt.Errorf("wait for stack %s service convergence: %w", request.StackName, err)
	}
	return nil
}

func (d *Deployer) reconcileResources(
	ctx context.Context,
	stackName,
	sourceComposePath string,
	desired compose.Compose,
) (resolvedResources, error) {
	resolved := resolvedResources{}
	if len(desired.Configs) == 0 && len(desired.Secrets) == 0 {
		return resolved, nil
	}

	data, err := d.secretResolver.Resolve(ctx, stackName, sourceComposePath, desired.Secrets)
	if err != nil {
		return resolved, fmt.Errorf("resolve secrets: %w", err)
	}
	resolved, err = d.resources.Reconcile(ctx, stackName, desired.Configs, desired.Secrets, data)
	secrets.Clear(data)
	if err != nil {
		return resolved, fmt.Errorf("reconcile configs and secrets: %w", err)
	}
	return resolved, nil
}

func (d *Deployer) runInitJobs(
	ctx context.Context,
	stackName string,
	services []compose.Service,
	resolved resolvedResources,
) ([]InitJobResult, error) {
	result := make([]InitJobResult, 0)
	for _, service := range services {
		for _, job := range service.InitJobs {
			execution, err := d.initJobRunner.Run(ctx, initjob.Spec{
				StackName: stackName, ServiceName: service.Name, DefaultNetwork: service.Networks.GetNames(),
				ServiceSecrets: service.Secrets, ServiceConfigs: service.Configs,
				ResolvedSecrets: resolved.secrets, ResolvedConfigs: resolved.configs, Job: job,
			})
			if !execution.StartedAt.IsZero() {
				result = append(result, execution)
			}
			if err != nil {
				return result, fmt.Errorf("service %s init job %s: %w", service.Name, job.Name, err)
			}
		}
	}
	return result, nil
}
