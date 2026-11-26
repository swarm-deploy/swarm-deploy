package usecase

import (
	"context"
	"errors"
	"fmt"

	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/selfheal"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/resources/service/modelstore"
)

// ServiceSelfHealChanger coordinates catalog selection and GitOps self-heal proposals.
type ServiceSelfHealChanger struct {
	catalog   modelstore.Store
	proposals *selfheal.Service
}

// NewServiceSelfHealChanger creates the use case from the catalog and GitOps writer.
func NewServiceSelfHealChanger(catalog modelstore.Store, proposals *selfheal.Service) *ServiceSelfHealChanger {
	return &ServiceSelfHealChanger{catalog: catalog, proposals: proposals}
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

// Prepare reads policy and prepares a proposal for an exact service.
func (c *ServiceSelfHealChanger) Prepare(
	ctx context.Context, stack, service string, desiredEnabled bool,
) (ServiceSelfHealPlan, error) {
	policy, err := c.proposals.Preview(ctx, stack, service, desiredEnabled)
	if err != nil {
		return ServiceSelfHealPlan{}, err
	}
	return ServiceSelfHealPlan{
		Stack: stack, Service: service, Policy: policy,
	}, nil
}

// Publish revalidates the exact catalog service and publishes against the current write base.
func (c *ServiceSelfHealChanger) Publish(
	ctx context.Context, plan ServiceSelfHealPlan, desiredEnabled bool, author string,
) (selfheal.Result, error) {
	if err := c.requireService(ctx, plan.Stack, plan.Service); err != nil {
		return selfheal.Result{}, err
	}
	return c.proposals.Publish(
		ctx, plan.Stack, plan.Service, plan.Policy, desiredEnabled, author,
	)
}

func (c *ServiceSelfHealChanger) requireService(ctx context.Context, stack, service string) error {
	_, found, err := c.catalog.Find(ctx, stack, service)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrServiceCatalogUnavailable, err)
	}
	if !found {
		return fmt.Errorf("%w: %s/%s", ErrServiceNotFound, stack, service)
	}
	return nil
}
