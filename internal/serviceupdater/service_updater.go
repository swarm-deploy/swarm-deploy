package serviceupdater

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/artarts36/swarm-deploy/internal/compose"
	"github.com/artarts36/swarm-deploy/internal/config"
	gitx "github.com/artarts36/swarm-deploy/internal/git"
	"github.com/artarts36/swarm-deploy/internal/githosting"
	"github.com/artarts36/swarm-deploy/internal/registry"
	"github.com/distribution/reference"
)

const (
	defaultCommitAuthorEmail = "swarm-deploy@localhost"
	defaultUserName          = "unknown-user"
)

var gitBranchUnsafePartRegex = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

// ImageVersionResolver resolves image versions in container registry.
type ImageVersionResolver interface {
	// ResolveActualVersion resolves an image tag in registry and returns normalized metadata.
	ResolveActualVersion(ctx context.Context, image string) (registry.ImageVersion, error)
}

// StacksProvider returns a current stack list.
type StacksProvider func() []config.StackSpec

// UpdateImageVersionInput contains update parameters.
type UpdateImageVersionInput struct {
	// StackName is a target stack name.
	StackName string
	// ServiceName is a target service name inside stack compose.
	ServiceName string
	// ImageVersion is a target image tag.
	ImageVersion string
	// Reason is a user prompt that requested the image update.
	Reason string
	// UserName is an authenticated user name from security context.
	UserName string
}

// UpdateImageVersionResult describes update execution result.
type UpdateImageVersionResult struct {
	// StackName is a target stack name.
	StackName string `json:"stack"`
	// ServiceName is a target service name.
	ServiceName string `json:"service"`
	// OldImage is a previous service image reference.
	OldImage string `json:"oldImage"`
	// NewImage is an updated service image reference.
	NewImage string `json:"newImage"`
	// BranchName is a created branch name.
	BranchName string `json:"branch"`
	// BranchURL is a URL to created branch.
	BranchURL string `json:"branchUrl"`
	// CommitHash is a pushed commit hash.
	CommitHash string `json:"commit"`
	// MergeRequestURL is an optional merge request URL.
	MergeRequestURL string `json:"mergeRequestUrl,omitempty"`
}

// ServiceUpdater updates service image version in push repository and creates merge request.
type ServiceUpdater struct {
	stacksProvider    StacksProvider
	repository        gitx.Repository
	imageResolver     ImageVersionResolver
	pushRepositoryURL string
	pushBaseBranch    string
	pushAPIToken      string

	mergeRequestProviders []githosting.Provider

	steps []serviceUpdateStep

	mu sync.Mutex
}

type serviceUpdateStep struct {
	Name   string
	Action func(ctx context.Context, session *updateImageVersionSession) error
}

// NewServiceUpdater creates service updater component.
func NewServiceUpdater(
	stacksProvider StacksProvider,
	repository gitx.Repository,
	imageResolver ImageVersionResolver,
	pushRepositoryURL string,
	pushBaseBranch string,
	pushAPIToken string,
	mergeRequestProviders []githosting.Provider,
) *ServiceUpdater {
	su := &ServiceUpdater{
		stacksProvider:        stacksProvider,
		repository:            repository,
		imageResolver:         imageResolver,
		pushRepositoryURL:     pushRepositoryURL,
		pushBaseBranch:        pushBaseBranch,
		pushAPIToken:          pushAPIToken,
		mergeRequestProviders: mergeRequestProviders,
	}

	su.steps = []serviceUpdateStep{
		{
			Name:   "0. validate stack and service",
			Action: su.step0ValidateStackAndService,
		},
		{
			Name:   "1. validate image exists",
			Action: su.step1ValidateImageExists,
		},
		{
			Name:   "2. create branch",
			Action: su.step2CreateBranch,
		},
		{
			Name:   "3. update version in compose file",
			Action: su.step3UpdateComposeImageVersion,
		},
		{
			Name:   "4. commit changes",
			Action: su.step4CommitChanges,
		},
		{
			Name:   "5. push changes",
			Action: su.step5PushChanges,
		},
	}

	if pushAPIToken != "" {
		su.steps = append(su.steps, serviceUpdateStep{
			Name:   "6. create merge request",
			Action: su.step6CreateMergeRequest,
		})
	}

	return su
}

// UpdateImageVersion validates image and updates compose file in push repository.
func (s *ServiceUpdater) UpdateImageVersion(
	ctx context.Context,
	rawInput UpdateImageVersionInput,
) (UpdateImageVersionResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	input, err := normalizeInput(rawInput)
	if err != nil {
		return UpdateImageVersionResult{}, err
	}

	session := &updateImageVersionSession{
		input: input,
	}

	var stepErr error

	for _, step := range s.steps {
		slog.InfoContext(ctx, "[service-updater] running step", slog.String("step.name", step.Name))

		stepErr = step.Action(ctx, session)
		if stepErr != nil {
			slog.ErrorContext(ctx, "[service-updater] step failed",
				slog.String("step.name", step.Name),
				slog.Any("err", stepErr),
			)

			break
		}
	}

	return UpdateImageVersionResult{
		StackName:       input.StackName,
		ServiceName:     input.ServiceName,
		OldImage:        session.currentImage,
		NewImage:        session.newImage,
		BranchName:      session.branchName,
		BranchURL:       session.branchURL,
		CommitHash:      session.commitHash,
		MergeRequestURL: session.mergeRequestURL,
	}, stepErr
}

func (s *ServiceUpdater) step0ValidateStackAndService(
	_ context.Context,
	session *updateImageVersionSession,
) error {
	stackSpec, err := s.resolveStack(session.input.StackName)
	if err != nil {
		return err
	}

	composePath := filepath.Join(s.repository.WorkingDir(), stackSpec.ComposeFile)
	composeFile, err := compose.Load(composePath)
	if err != nil {
		return fmt.Errorf("load compose for stack %q: %w", stackSpec.Name, err)
	}

	currentImage, err := resolveServiceImage(composeFile, session.input.ServiceName)
	if err != nil {
		return fmt.Errorf("resolve service %q in stack %q: %w", session.input.ServiceName, stackSpec.Name, err)
	}

	newImage, err := buildImageWithVersion(currentImage, session.input.ImageVersion)
	if err != nil {
		return fmt.Errorf("build new image reference: %w", err)
	}

	if newImage == currentImage {
		return fmt.Errorf(
			"service %q in stack %q already uses image version %q",
			session.input.ServiceName,
			stackSpec.Name,
			session.input.ImageVersion,
		)
	}

	session.composePath = composePath
	session.composeFile = composeFile
	session.currentImage = currentImage
	session.newImage = newImage

	return nil
}

func (s *ServiceUpdater) step1ValidateImageExists(
	ctx context.Context,
	session *updateImageVersionSession,
) error {
	_, err := s.imageResolver.ResolveActualVersion(ctx, session.newImage)
	if err != nil {
		return fmt.Errorf("resolve image %q in registry: %w", session.newImage, err)
	}

	return nil
}

func (s *ServiceUpdater) step2CreateBranch(
	ctx context.Context,
	session *updateImageVersionSession,
) error {
	branchName, err := buildBranchName(session.input.ServiceName, session.input.ImageVersion)
	if err != nil {
		return err
	}

	branch, err := s.repository.Branch(ctx, branchName)
	if err != nil {
		return fmt.Errorf("create branch %q: %w", branchName, err)
	}

	session.branch = branch
	session.branchName = branchName

	branchURL, err := buildBranchURL(s.pushRepositoryURL, session.branchName)
	if err != nil {
		return fmt.Errorf("build branch url: %w", err)
	}

	session.branchURL = branchURL

	return nil
}

func (s *ServiceUpdater) step3UpdateComposeImageVersion(
	_ context.Context,
	session *updateImageVersionSession,
) error {
	err := setServiceImage(session.composeFile, session.input.ServiceName, session.newImage)
	if err != nil {
		return fmt.Errorf("set service image in compose: %w", err)
	}

	payload, err := session.composeFile.MarshalYAML()
	if err != nil {
		return fmt.Errorf("marshal compose yaml: %w", err)
	}

	err = os.WriteFile(session.composePath, payload, 0o600)
	if err != nil {
		return fmt.Errorf("write compose file %q: %w", session.composePath, err)
	}

	composeRelativePath, err := filepath.Rel(s.repository.WorkingDir(), session.composePath)
	if err != nil {
		return fmt.Errorf("resolve compose relative path: %w", err)
	}

	session.composeRelativePath = filepath.ToSlash(composeRelativePath)
	return nil
}

func (s *ServiceUpdater) step4CommitChanges(
	ctx context.Context,
	session *updateImageVersionSession,
) error {
	if err := s.repository.Add(ctx, session.composeRelativePath); err != nil {
		return fmt.Errorf("stage compose file %q: %w", session.composeRelativePath, err)
	}

	commitHash, err := s.repository.Commit(
		ctx,
		buildMergeRequestTitle(session.input.ServiceName, session.input.ImageVersion),
		gitx.CommitAuthor{
			Name:  session.input.UserName,
			Email: defaultCommitAuthorEmail,
		},
	)
	if err != nil {
		return fmt.Errorf("commit compose update: %w", err)
	}

	session.commitHash = commitHash
	return nil
}

func (s *ServiceUpdater) step5PushChanges(
	ctx context.Context,
	session *updateImageVersionSession,
) error {
	if err := s.repository.Push(ctx, session.branchName); err != nil {
		return fmt.Errorf("push branch %q: %w", session.branchName, err)
	}

	return nil
}

func (s *ServiceUpdater) step6CreateMergeRequest(
	ctx context.Context,
	session *updateImageVersionSession,
) error {
	provider := s.resolveMergeRequestProvider()
	if provider == nil {
		return nil
	}

	mergeRequestURL, err := provider.CreateMergeRequest(ctx, githosting.CreateMergeRequestRequest{
		RepositoryURL: s.pushRepositoryURL,
		BaseBranch:    s.pushBaseBranch,
		HeadBranch:    session.branchName,
		Title:         buildMergeRequestTitle(session.input.ServiceName, session.input.ImageVersion),
		Body:          fmt.Sprintf("%s by %s", session.input.Reason, session.input.UserName),
		Token:         s.pushAPIToken,
	})
	if err != nil {
		return fmt.Errorf("create merge request: %w", err)
	}

	session.mergeRequestURL = mergeRequestURL
	return nil
}

func (s *ServiceUpdater) resolveMergeRequestProvider() githosting.Provider {
	for _, provider := range s.mergeRequestProviders {
		if provider.Supports(s.pushRepositoryURL) {
			return provider
		}
	}

	return nil
}

func normalizeInput(rawInput UpdateImageVersionInput) (UpdateImageVersionInput, error) {
	input := UpdateImageVersionInput{
		StackName:    strings.TrimSpace(rawInput.StackName),
		ServiceName:  strings.TrimSpace(rawInput.ServiceName),
		ImageVersion: strings.TrimSpace(rawInput.ImageVersion),
		Reason:       strings.TrimSpace(rawInput.Reason),
		UserName:     strings.TrimSpace(rawInput.UserName),
	}

	if input.StackName == "" {
		return UpdateImageVersionInput{}, errors.New("stack is required")
	}
	if input.ServiceName == "" {
		return UpdateImageVersionInput{}, errors.New("service is required")
	}
	if input.ImageVersion == "" {
		return UpdateImageVersionInput{}, errors.New("imageVersion is required")
	}
	if input.Reason == "" {
		return UpdateImageVersionInput{}, errors.New("reason is required")
	}
	if input.UserName == "" {
		input.UserName = defaultUserName
	}

	return input, nil
}

func (s *ServiceUpdater) resolveStack(stackName string) (config.StackSpec, error) {
	stacks := s.stacksProvider()
	if len(stacks) == 0 {
		return config.StackSpec{}, errors.New("stacks provider returned empty stack list")
	}

	for i, stack := range stacks {
		stack.Name = strings.TrimSpace(stack.Name)
		stack.ComposeFile = strings.TrimSpace(stack.ComposeFile)
		if stack.Name == "" {
			return config.StackSpec{}, fmt.Errorf("stacks[%d].name is required", i)
		}
		if stack.ComposeFile == "" {
			return config.StackSpec{}, fmt.Errorf("stacks[%d].composeFile is required", i)
		}
		if stack.Name == stackName {
			return stack, nil
		}
	}

	return config.StackSpec{}, fmt.Errorf("stack %q not found", stackName)
}

func resolveServiceImage(file *compose.File, serviceName string) (string, error) {
	for _, service := range file.Services {
		if service.Name != serviceName {
			continue
		}
		if strings.TrimSpace(service.Image) == "" {
			return "", fmt.Errorf("service %q does not have image field", serviceName)
		}

		return strings.TrimSpace(service.Image), nil
	}

	return "", fmt.Errorf("service %q not found", serviceName)
}

func buildImageWithVersion(image string, version string) (string, error) {
	named, err := reference.ParseNormalizedNamed(strings.TrimSpace(image))
	if err != nil {
		return "", fmt.Errorf("parse image reference %q: %w", image, err)
	}

	tagged, err := reference.WithTag(reference.TrimNamed(named), strings.TrimSpace(version))
	if err != nil {
		return "", fmt.Errorf("set image version %q: %w", version, err)
	}

	return reference.FamiliarString(tagged), nil
}

func setServiceImage(file *compose.File, serviceName string, image string) error {
	servicesMap, ok := file.RawMap["services"].(map[string]any)
	if !ok {
		return errors.New("compose file does not contain services map")
	}

	serviceRaw, ok := servicesMap[serviceName]
	if !ok {
		return fmt.Errorf("service %q not found in compose map", serviceName)
	}

	serviceMap, ok := serviceRaw.(map[string]any)
	if !ok {
		return fmt.Errorf("compose services.%s must be a map", serviceName)
	}

	serviceMap["image"] = image
	return nil
}

func buildBranchName(serviceName string, imageVersion string) (string, error) {
	serviceNamePart := sanitizeBranchPart(serviceName)
	if serviceNamePart == "" {
		return "", fmt.Errorf("service %q can not be converted to git branch name", serviceName)
	}

	versionPart := sanitizeBranchPart(imageVersion)
	if versionPart == "" {
		return "", fmt.Errorf("imageVersion %q can not be converted to git branch name", imageVersion)
	}

	return fmt.Sprintf("%s-up-image-%s", serviceNamePart, versionPart), nil
}

func sanitizeBranchPart(raw string) string {
	sanitized := gitBranchUnsafePartRegex.ReplaceAllString(strings.TrimSpace(raw), "-")
	return strings.Trim(sanitized, "-")
}

func buildMergeRequestTitle(serviceName string, imageVersion string) string {
	return fmt.Sprintf("chore(%s): up image to %s", serviceName, imageVersion)
}

type updateImageVersionSession struct {
	input UpdateImageVersionInput

	composePath         string
	composeRelativePath string
	composeFile         *compose.File

	currentImage string
	newImage     string

	branch          gitx.Repository
	branchName      string
	branchURL       string
	commitHash      string
	mergeRequestURL string
}
