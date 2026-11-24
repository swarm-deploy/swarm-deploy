package usecase

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/selfheal"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/resources/service/model"
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

// AmbiguousServiceTargetError provides the matching services for a caller to choose from.
type AmbiguousServiceTargetError struct {
	// Candidates are sorted, fully qualified stack/service names.
	Candidates []string
}

func (e *AmbiguousServiceTargetError) Error() string {
	return "multiple matching services: " + strings.Join(e.Candidates, ", ")
}

// Prepare resolves a target and reads the current policy without publishing a change.
func (e *ServiceSelfHealEnabler) Prepare(ctx context.Context, target string) (ServiceSelfHealPlan, error) {
	services, err := e.catalog.ReadAll(ctx)
	if err != nil {
		return ServiceSelfHealPlan{}, fmt.Errorf("%w: %w", ErrServiceCatalogUnavailable, err)
	}
	matches := model.MatchServiceTarget(services, target)
	if len(matches) == 0 {
		return ServiceSelfHealPlan{}, fmt.Errorf("%w: %q", ErrServiceNotFound, target)
	}
	if len(matches) > 1 {
		return ServiceSelfHealPlan{}, &AmbiguousServiceTargetError{Candidates: model.QualifiedNames(matches)}
	}
	service := matches[0]
	policy, err := e.proposals.Preview(ctx, service.Stack, service.Name)
	if err != nil {
		return ServiceSelfHealPlan{}, err
	}
	return ServiceSelfHealPlan{Stack: service.Stack, Service: service.Name, Policy: policy}, nil
}

// Publish revalidates the exact catalog service and publishes against the current write base.
func (e *ServiceSelfHealEnabler) Publish(
	ctx context.Context, plan ServiceSelfHealPlan, author string,
) (selfheal.Result, error) {
	_, found, err := e.catalog.Find(ctx, plan.Stack, plan.Service)
	if err != nil {
		return selfheal.Result{}, fmt.Errorf("%w: %w", ErrServiceCatalogUnavailable, err)
	}
	if !found {
		return selfheal.Result{}, fmt.Errorf("%w: %s/%s", ErrServiceNotFound, plan.Stack, plan.Service)
	}
	return e.proposals.Enable(ctx, plan.Stack, plan.Service, plan.Policy, author)
}
