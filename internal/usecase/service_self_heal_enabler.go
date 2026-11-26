package usecase

import (
	"context"
	"errors"
	"fmt"

	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/selfheal"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/resources/service/modelstore"
)

// ServiceSelfHealEnabler coordinates catalog selection and GitOps self-heal proposals.
type ServiceSelfHealEnabler struct {
	catalog   modelstore.Store
	proposals *selfheal.Service
}

// NewServiceSelfHealEnabler creates the use case from the catalog and GitOps writer.
func NewServiceSelfHealEnabler(catalog modelstore.Store, proposals *selfheal.Service) *ServiceSelfHealEnabler {
	return &ServiceSelfHealEnabler{catalog: catalog, proposals: proposals}
}

// ServiceSelfHealPlan identifies the exact service and policy observed during preparation.
type ServiceSelfHealPlan struct {
	// Stack is the canonical stack name.
	Stack string `json:"stack"`
	// Service is the canonical service name.
	Service string `json:"service"`
	// Policy is the effective policy at the proposal base.
	Policy selfheal.State `json:"policy"`
}

// ErrServiceCatalogUnavailable indicates a failed catalog read during selection or publication.
var ErrServiceCatalogUnavailable = errors.New("could not read service catalog")

// ErrServiceNotFound indicates that the target is absent from the catalog.
var ErrServiceNotFound = errors.New("service not found in catalog")

// Prepare reads policy for an exact service without publishing a change.
func (e *ServiceSelfHealEnabler) Prepare(
	ctx context.Context, stack, service string,
) (ServiceSelfHealPlan, error) {
	policy, err := e.proposals.Preview(ctx, stack, service)
	if err != nil {
		return ServiceSelfHealPlan{}, err
	}
	return ServiceSelfHealPlan{Stack: stack, Service: service, Policy: policy}, nil
}

// PrepareDisable reads policy and prepares a proposal to disable self-heal for an exact service.
func (e *ServiceSelfHealEnabler) PrepareDisable(
	ctx context.Context, stack, service string,
) (ServiceSelfHealPlan, error) {
	policy, err := e.proposals.PreviewDisable(ctx, stack, service)
	if err != nil {
		return ServiceSelfHealPlan{}, err
	}
	return ServiceSelfHealPlan{Stack: stack, Service: service, Policy: policy}, nil
}

// Publish revalidates the exact catalog service and publishes against the current write base.
func (e *ServiceSelfHealEnabler) Publish(
	ctx context.Context, plan ServiceSelfHealPlan, author string,
) (selfheal.Result, error) {
	if err := e.requireService(ctx, plan.Stack, plan.Service); err != nil {
		return selfheal.Result{}, err
	}
	return e.proposals.Enable(ctx, plan.Stack, plan.Service, plan.Policy, author)
}

// PublishDisable revalidates the exact service and publishes a proposal to disable self-heal.
func (e *ServiceSelfHealEnabler) PublishDisable(
	ctx context.Context, plan ServiceSelfHealPlan, author string,
) (selfheal.Result, error) {
	if err := e.requireService(ctx, plan.Stack, plan.Service); err != nil {
		return selfheal.Result{}, err
	}
	return e.proposals.Disable(ctx, plan.Stack, plan.Service, plan.Policy, author)
}

func (e *ServiceSelfHealEnabler) requireService(ctx context.Context, stack, service string) error {
	_, found, err := e.catalog.Find(ctx, stack, service)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrServiceCatalogUnavailable, err)
	}
	if !found {
		return fmt.Errorf("%w: %s/%s", ErrServiceNotFound, stack, service)
	}
	return nil
}
