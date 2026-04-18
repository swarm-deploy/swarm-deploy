package inspector

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"log/slog"
	"strconv"
	"strings"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/docker/docker/api/types/container"
	dockerswarm "github.com/docker/docker/api/types/swarm"
)

const (
	defaultServiceLogsLimit           = 200
	dockerLogFrameHeaderSize          = 8
	serviceLogsScannerInitialBufSize  = 64 * 1024
	serviceLogsScannerMaxTokenBufSize = 1 << 20
)

// InspectServiceStatus returns compact status snapshot for a stack service.
func (i *Inspector) InspectServiceStatus(ctx context.Context, stackName, serviceName string) (ServiceStatus, error) {
	service, err := i.inspectService(ctx, stackName, serviceName)
	if err != nil {
		return ServiceStatus{}, err
	}

	return ServiceStatus{
		Stack:   stackName,
		Service: serviceName,
		Spec:    toServiceSpec(service.Spec),
	}, nil
}

// InspectServiceSpec returns full compact service projection for a stack service.
func (i *Inspector) InspectServiceSpec(ctx context.Context, stackName, serviceName string) (Service, error) {
	service, err := i.inspectService(ctx, stackName, serviceName)
	if err != nil {
		return Service{}, err
	}

	return Service{
		ID:           service.ID,
		CreatedAt:    service.CreatedAt,
		UpdatedAt:    service.UpdatedAt,
		Secrets:      toServiceSecrets(service.Spec.TaskTemplate.ContainerSpec),
		Spec:         toServiceSpec(service.Spec),
		PreviousSpec: toPreviousServiceSpec(service.PreviousSpec),
		UpdateStatus: toServiceUpdateStatus(service.UpdateStatus),
	}, nil
}

func (i *Inspector) inspectService(ctx context.Context, stackName, serviceName string) (dockerswarm.Service, error) {
	fullServiceName := fmt.Sprintf("%s_%s", stackName, serviceName)
	service, _, err := i.dockerClient.ServiceInspectWithRaw(ctx, fullServiceName, dockerswarm.ServiceInspectOptions{})
	if err != nil {
		if cerrdefs.IsNotFound(err) {
			return dockerswarm.Service{}, ErrServiceNotFound
		}
		return dockerswarm.Service{}, fmt.Errorf("inspect service %s: %w", fullServiceName, err)
	}

	return service, nil
}

// InspectServiceLabels returns service, container and image labels for a stack service.
func (i *Inspector) InspectServiceLabels(
	ctx context.Context,
	stackName, serviceName string,
) (ServiceLabels, error) {
	fullServiceName := fmt.Sprintf("%s_%s", stackName, serviceName)
	service, _, err := i.dockerClient.ServiceInspectWithRaw(ctx, fullServiceName, dockerswarm.ServiceInspectOptions{})
	if err != nil {
		if cerrdefs.IsNotFound(err) {
			return ServiceLabels{}, ErrServiceNotFound
		}
		return ServiceLabels{}, fmt.Errorf("inspect service %s: %w", fullServiceName, err)
	}

	labels := ServiceLabels{
		Service: cloneStringMap(service.Spec.Labels),
	}

	containerSpec := service.Spec.TaskTemplate.ContainerSpec
	if containerSpec != nil {
		labels.Container = cloneStringMap(containerSpec.Labels)
		labels.ContainerEnv = cloneStringSlice(containerSpec.Env)
	}

	imageRef := ""
	if containerSpec != nil {
		imageRef = containerSpec.Image
	}

	slog.DebugContext(ctx, "[swarm] inspecting image", slog.String("image_ref", imageRef))

	image, err := i.dockerClient.ImageInspect(ctx, imageRef)
	if err != nil {
		if cerrdefs.IsNotFound(err) {
			slog.DebugContext(ctx, "[swarm] image not found", slog.String("image_ref", imageRef))

			return labels, nil
		}
		return labels, fmt.Errorf("inspect image %s: %w", imageRef, err)
	}

	slog.DebugContext(ctx, "[swarm] image inspected",
		slog.String("image_ref", imageRef),
		slog.Any("image", image),
	)

	if image.Config != nil {
		labels.Image = cloneStringMap(image.Config.Labels)
	}
	return labels, nil
}

// ServiceLogsOptions configures stack service logs query.
type ServiceLogsOptions struct {
	// Limit is max number of latest lines to return.
	Limit int
	// Since defines lower bound for log timestamps.
	Since *time.Time
	// Until defines upper bound for log timestamps.
	Until *time.Time
}

// InspectServiceLogs returns recent logs for a stack service.
func (i *Inspector) InspectServiceLogs(
	ctx context.Context,
	stackName string,
	serviceName string,
	options ServiceLogsOptions,
) ([]string, error) {
	fullServiceName := fmt.Sprintf("%s_%s", stackName, serviceName)
	reader, err := i.dockerClient.ServiceLogs(ctx, fullServiceName, buildDockerServiceLogsOptions(options))
	if err != nil {
		if cerrdefs.IsNotFound(err) {
			return nil, ErrServiceNotFound
		}

		return nil, fmt.Errorf("read logs for service %s: %w", fullServiceName, err)
	}
	defer reader.Close()

	rawLogs, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("read logs stream for service %s: %w", fullServiceName, err)
	}

	decodedLogs := demultiplexDockerLogStream(rawLogs)

	logs := make([]string, 0)

	scanner := bufio.NewScanner(bytes.NewReader(decodedLogs))
	scanner.Buffer(
		make([]byte, 0, serviceLogsScannerInitialBufSize),
		serviceLogsScannerMaxTokenBufSize,
	)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		logs = append(logs, line)
	}
	if scanErr := scanner.Err(); scanErr != nil {
		return nil, fmt.Errorf("scan logs for service %s: %w", fullServiceName, scanErr)
	}

	return logs, nil
}

func buildDockerServiceLogsOptions(options ServiceLogsOptions) container.LogsOptions {
	limit := options.Limit
	if limit <= 0 {
		limit = defaultServiceLogsLimit
	}

	logsOptions := container.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Timestamps: true,
		Tail:       strconv.Itoa(limit),
	}

	if options.Since != nil {
		logsOptions.Since = options.Since.UTC().Format(time.RFC3339Nano)
	}
	if options.Until != nil {
		logsOptions.Until = options.Until.UTC().Format(time.RFC3339Nano)
	}

	return logsOptions
}

func demultiplexDockerLogStream(raw []byte) []byte {
	// Docker multiplexed logs use 8-byte frame headers:
	// [stream(1)][0][0][0][payload_size_be_uint32].
	// If payload is shorter than a single header, treat it as plain text.
	if len(raw) < dockerLogFrameHeaderSize {
		return raw
	}

	decoded := bytes.NewBuffer(make([]byte, 0, len(raw)))
	cursor := 0
	parsedFrames := false

	for cursor+dockerLogFrameHeaderSize <= len(raw) {
		header := raw[cursor : cursor+dockerLogFrameHeaderSize]
		// Non-zero reserved bytes mean this is not a Docker multiplexed frame.
		// If this happens before any parsed frame, keep stream untouched.
		// If we already parsed something, append the remainder as best-effort.
		if header[1] != 0 || header[2] != 0 || header[3] != 0 {
			if !parsedFrames {
				return raw
			}

			decoded.Write(raw[cursor:])
			return decoded.Bytes()
		}

		frameSize := int(binary.BigEndian.Uint32(header[4:dockerLogFrameHeaderSize]))
		cursor += dockerLogFrameHeaderSize

		// Broken frame length: keep behavior safe and non-destructive.
		if frameSize < 0 || cursor+frameSize > len(raw) {
			if !parsedFrames {
				return raw
			}

			decoded.Write(raw[cursor-dockerLogFrameHeaderSize:])
			return decoded.Bytes()
		}

		if frameSize > 0 {
			decoded.Write(raw[cursor : cursor+frameSize])
		}
		cursor += frameSize
		parsedFrames = true
	}

	// Preserve trailing bytes that don't form a full header.
	if cursor < len(raw) {
		decoded.Write(raw[cursor:])
	}
	// If we did not recognize a single frame, return original stream.
	if !parsedFrames {
		return raw
	}

	return decoded.Bytes()
}

func toServiceSpec(spec dockerswarm.ServiceSpec) ServiceSpec {
	mode, replicas := resolveServiceDeployMode(spec.Mode)

	mapped := ServiceSpec{
		Mode:     mode,
		Replicas: replicas,
		Labels:   cloneStringMap(spec.Labels),
		Network:  toServiceNetworks(spec.TaskTemplate.Networks),
	}

	containerSpec := spec.TaskTemplate.ContainerSpec
	if containerSpec != nil {
		mapped.Image = containerSpec.Image
		mapped.Secrets = toServiceSecrets(containerSpec)
	}

	if resources := spec.TaskTemplate.Resources; resources != nil && resources.Reservations != nil {
		mapped.RequestedRAMBytes = resources.Reservations.MemoryBytes
		mapped.RequestedCPUNano = resources.Reservations.NanoCPUs
	}
	if resources := spec.TaskTemplate.Resources; resources != nil && resources.Limits != nil {
		mapped.LimitRAMBytes = resources.Limits.MemoryBytes
		mapped.LimitCPUNano = resources.Limits.NanoCPUs
	}

	return mapped
}

func resolveServiceDeployMode(mode dockerswarm.ServiceMode) (string, uint64) {
	switch {
	case mode.Replicated != nil:
		replicas := uint64(0)
		if mode.Replicated.Replicas != nil {
			replicas = *mode.Replicated.Replicas
		}
		return "replicated", replicas
	case mode.Global != nil:
		return "global", 0
	case mode.ReplicatedJob != nil:
		replicas := uint64(0)
		if mode.ReplicatedJob.MaxConcurrent != nil {
			replicas = *mode.ReplicatedJob.MaxConcurrent
		}
		return "replicated-job", replicas
	case mode.GlobalJob != nil:
		return "global-job", 0
	default:
		return "unknown", 0
	}
}

func toPreviousServiceSpec(previous *dockerswarm.ServiceSpec) *ServiceSpec {
	if previous == nil {
		return nil
	}

	mapped := toServiceSpec(*previous)

	return &mapped
}

func toServiceUpdateStatus(status *dockerswarm.UpdateStatus) *ServiceUpdateStatus {
	if status == nil {
		return nil
	}

	startedAt := time.Time{}
	if status.StartedAt != nil {
		startedAt = *status.StartedAt
	}

	completedAt := time.Time{}
	if status.CompletedAt != nil {
		completedAt = *status.CompletedAt
	}

	return &ServiceUpdateStatus{
		State:       string(status.State),
		StartedAt:   startedAt,
		CompletedAt: completedAt,
		Message:     status.Message,
	}
}

func toServiceSecrets(containerSpec *dockerswarm.ContainerSpec) []ServiceSecret {
	if containerSpec == nil || len(containerSpec.Secrets) == 0 {
		return nil
	}

	mapped := make([]ServiceSecret, 0, len(containerSpec.Secrets))
	for _, secret := range containerSpec.Secrets {
		if secret == nil {
			continue
		}

		target := ""
		if secret.File != nil {
			target = secret.File.Name
		}

		secretName := secret.SecretName
		if secretName == "" {
			secretName = secret.SecretID
		}

		mapped = append(mapped, ServiceSecret{
			SecretID:   secret.SecretID,
			SecretName: secretName,
			Target:     target,
		})
	}
	if len(mapped) == 0 {
		return nil
	}

	return mapped
}

func toServiceNetworks(networks []dockerswarm.NetworkAttachmentConfig) []ServiceNetwork {
	if len(networks) == 0 {
		return nil
	}

	mapped := make([]ServiceNetwork, 0, len(networks))
	for _, network := range networks {
		mapped = append(mapped, ServiceNetwork{
			Target:  network.Target,
			Aliases: cloneStringSlice(network.Aliases),
		})
	}

	return mapped
}

func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}

	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func cloneStringSlice(in []string) []string {
	if len(in) == 0 {
		return nil
	}

	out := make([]string, len(in))
	copy(out, in)

	return out
}
