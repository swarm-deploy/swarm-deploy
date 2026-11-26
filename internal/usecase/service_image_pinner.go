package usecase

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/distribution/reference"
	"github.com/opencontainers/go-digest"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/gitops/imagepin"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/resources/service/modelstore"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
)

// ServiceImagePinStatus describes whether the running image can be pinned safely.
type ServiceImagePinStatus string

const (
	// ServiceImagePinReady means the exact proposed image can be confirmed.
	ServiceImagePinReady ServiceImagePinStatus = "ready"
	// ServiceImagePinAlreadyPinned means Git already contains the running digest.
	ServiceImagePinAlreadyPinned ServiceImagePinStatus = "already_pinned"
	// ServiceImagePinMixedRollout means running tasks use multiple digests.
	ServiceImagePinMixedRollout ServiceImagePinStatus = "mixed_rollout"
	// ServiceImagePinNoRunningTasks means no task is currently running.
	ServiceImagePinNoRunningTasks ServiceImagePinStatus = "no_running_tasks"
	// ServiceImagePinDigestUnavailable means at least one running task lacks a trustworthy digest.
	ServiceImagePinDigestUnavailable ServiceImagePinStatus = "digest_unavailable"
	// ServiceImagePinGitMismatch means the Compose image does not match the running image identity.
	ServiceImagePinGitMismatch ServiceImagePinStatus = "git_mismatch"
)

// ImageCount is a deterministic task count for one image value or digest.
type ImageCount struct {
	// Value is an image reference or digest.
	Value string `json:"value"`
	// Count is the number of running tasks with the value.
	Count int `json:"count"`
}

// ServiceImagePinPlan is the exact mutation shown for confirmation.
type ServiceImagePinPlan struct {
	// Stack is the canonical stack name.
	Stack string `json:"stack"`
	// Service is the canonical service name.
	Service string `json:"service"`
	// RunningTasks is the number of running tasks used as evidence.
	RunningTasks int `json:"runningTasks"`
	// CurrentImage is the exact semantic image value read from Git.
	CurrentImage string `json:"currentImage"`
	// ProposedImage preserves CurrentImage and appends the observed digest.
	ProposedImage string `json:"proposedImage"`
	// Digest is the common digest of all running tasks.
	Digest string `json:"digest"`
}

// ServiceImagePinAssessment contains preparation evidence and any safe proposal.
type ServiceImagePinAssessment struct {
	// Status is the preparation outcome.
	Status ServiceImagePinStatus
	// Plan is populated for ready and already-pinned outcomes.
	Plan ServiceImagePinPlan
	// DigestCounts contains running replica counts by digest.
	DigestCounts []ImageCount
	// ImageCounts contains running replica counts by complete task image reference.
	ImageCounts []ImageCount
	// InvalidTasks is the number of running tasks without a trustworthy digest.
	InvalidTasks int
}

// ErrImagePinPlanChanged means the confirmed Git or runtime evidence is no longer current.
var ErrImagePinPlanChanged = errors.New("service image pin evidence changed after preparation")

// ServiceImagePinner coordinates catalog validation, Swarm task evidence, and GitOps publication.
type ServiceImagePinner struct {
	catalog   modelstore.Store
	services  swarm.ServiceManager
	proposals *imagepin.Service
}

// NewServiceImagePinner creates the application workflow for pinning a running image in Git.
func NewServiceImagePinner(
	catalog modelstore.Store, services swarm.ServiceManager, proposals *imagepin.Service,
) *ServiceImagePinner {
	return &ServiceImagePinner{catalog: catalog, services: services, proposals: proposals}
}

// Prepare reads Compose first and then assesses every currently running task.
func (p *ServiceImagePinner) Prepare(
	ctx context.Context, stack, service string,
) (ServiceImagePinAssessment, error) {
	source, err := p.proposals.Preview(ctx, stack, service)
	if err != nil {
		return ServiceImagePinAssessment{}, err
	}
	tasks, err := p.services.ListTasks(ctx, swarm.NewServiceReference(stack, service).Name())
	if err != nil {
		return ServiceImagePinAssessment{}, fmt.Errorf("list service tasks: %w", err)
	}
	return assessServiceImage(stack, service, source.Image, tasks)
}

// Publish revalidates the exact service, Compose image, and running tasks before publishing.
func (p *ServiceImagePinner) Publish(
	ctx context.Context, plan ServiceImagePinPlan, author string,
) (imagepin.Result, error) {
	if err := p.requireService(ctx, plan.Stack, plan.Service); err != nil {
		return imagepin.Result{}, err
	}
	assessment, err := p.Prepare(ctx, plan.Stack, plan.Service)
	if err != nil {
		return imagepin.Result{}, err
	}
	if assessment.Status == ServiceImagePinAlreadyPinned &&
		assessment.Plan.CurrentImage == plan.ProposedImage {
		return imagepin.Result{AlreadyPinned: true}, nil
	}
	if assessment.Status != ServiceImagePinReady || assessment.Plan != plan {
		return imagepin.Result{}, ErrImagePinPlanChanged
	}
	result, err := p.proposals.Publish(
		ctx, plan.Stack, plan.Service, plan.CurrentImage, plan.ProposedImage, author,
	)
	if errors.Is(err, imagepin.ErrImageChanged) {
		return imagepin.Result{}, ErrImagePinPlanChanged
	}
	return result, err
}

func (p *ServiceImagePinner) requireService(ctx context.Context, stack, service string) error {
	_, found, err := p.catalog.Find(ctx, stack, service)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrServiceCatalogUnavailable, err)
	}
	if !found {
		return fmt.Errorf("%w: %s/%s", ErrServiceNotFound, stack, service)
	}
	return nil
}

type parsedImage struct {
	repository, tag, digest string
	pinned                  bool
}

func assessServiceImage(
	stack, service, currentImage string, tasks []swarm.ServiceTask,
) (ServiceImagePinAssessment, error) {
	gitImage, err := parseImage(currentImage, false)
	if err != nil {
		return ServiceImagePinAssessment{}, fmt.Errorf("parse Compose image %q: %w", currentImage, err)
	}
	running := make([]swarm.ServiceTask, 0, len(tasks))
	for _, task := range tasks {
		if task.CurrentState == swarm.TaskStateRunning {
			running = append(running, task)
		}
	}
	base := ServiceImagePinPlan{Stack: stack, Service: service, RunningTasks: len(running), CurrentImage: currentImage}
	if len(running) == 0 {
		return ServiceImagePinAssessment{Status: ServiceImagePinNoRunningTasks, Plan: base}, nil
	}

	digests := map[string]int{}
	images := map[string]int{}
	parsed := make([]parsedImage, 0, len(running))
	invalid := 0
	for _, task := range running {
		image := task.Spec.Image()
		images[image]++
		actual, parseErr := parseImage(image, true)
		if parseErr != nil {
			invalid++
			continue
		}
		parsed = append(parsed, actual)
		digests[actual.digest]++
	}
	assessment := ServiceImagePinAssessment{
		Plan: base, DigestCounts: sortedCounts(digests), ImageCounts: sortedCounts(images), InvalidTasks: invalid,
	}
	if invalid > 0 {
		assessment.Status = ServiceImagePinDigestUnavailable
		return assessment, nil
	}
	if len(digests) > 1 {
		assessment.Status = ServiceImagePinMixedRollout
		return assessment, nil
	}

	digestValue := parsed[0].digest
	base.Digest = digestValue
	assessment.Plan = base
	for _, actual := range parsed {
		if !sameImageIdentity(gitImage, actual) {
			assessment.Status = ServiceImagePinGitMismatch
			return assessment, nil
		}
	}
	if gitImage.pinned {
		if gitImage.digest != digestValue {
			assessment.Status = ServiceImagePinGitMismatch
			return assessment, nil
		}
		assessment.Status = ServiceImagePinAlreadyPinned
		return assessment, nil
	}
	base.ProposedImage = currentImage + "@" + digestValue
	assessment.Status = ServiceImagePinReady
	assessment.Plan = base
	return assessment, nil
}

func parseImage(value string, requireDigest bool) (parsedImage, error) {
	raw := strings.TrimSpace(value)
	named, err := reference.ParseNormalizedNamed(raw)
	if err != nil {
		return parsedImage{}, err
	}
	result := parsedImage{repository: named.Name()}
	if tagged, ok := named.(reference.NamedTagged); ok {
		result.tag = tagged.Tag()
	}
	if canonical, ok := named.(reference.Canonical); ok {
		result.pinned = true
		result.digest = canonical.Digest().String()
		if canonical.Digest().Algorithm() != digest.SHA256 || canonical.Digest().Validate() != nil {
			return parsedImage{}, errors.New("image does not contain a valid sha256 digest")
		}
	}
	if requireDigest && !result.pinned {
		return parsedImage{}, errors.New("image does not contain a digest")
	}
	if !result.pinned && result.tag == "" {
		result.tag = "latest"
	}
	return result, nil
}

func sameImageIdentity(gitImage, actual parsedImage) bool {
	if gitImage.repository != actual.repository {
		return false
	}
	if gitImage.tag != "" {
		return gitImage.tag == actual.tag
	}
	return gitImage.pinned
}

func sortedCounts(counts map[string]int) []ImageCount {
	result := make([]ImageCount, 0, len(counts))
	for value, count := range counts {
		result = append(result, ImageCount{Value: value, Count: count})
	}
	slices.SortFunc(result, func(left, right ImageCount) int { return strings.Compare(left.Value, right.Value) })
	return result
}
