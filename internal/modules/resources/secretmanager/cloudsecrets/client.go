//go:generate mockgen -source=client.go -destination=mock_controller.go -package=cloudsecrets

package cloudsecrets

import (
	"context"
	"time"

	cloudsecretspb "github.com/swarm-deploy/swarm-deploy/internal/modules/resources/secretmanager/cloudsecrets/pb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const (
	getInfoTimeout = 3 * time.Second
	syncTimeout    = 5 * time.Minute
)

// Info describes the runtime configuration reported by cloud-secrets.
type Info struct {
	// Version is the running cloud-secrets version.
	Version string
	// ProviderName is the configured provider display name.
	ProviderName string
	// ProviderLink points to the provider management UI when available.
	ProviderLink string
	// LastSyncAt is the last successful synchronization known by this process.
	LastSyncAt *time.Time
	// NextSyncAt is the next synchronization scheduled by this process.
	NextSyncAt *time.Time
}

// SyncResult summarizes a cloud-secrets synchronization.
type SyncResult struct {
	// Created is the number of newly created logical secrets.
	Created uint32
	// Updated is the number of updated logical secrets.
	Updated uint32
	// Removed is the number of removed logical secrets.
	Removed uint32
	// Unchanged is the number of unchanged logical secrets.
	Unchanged uint32
}

// Controller controls one cloud-secrets instance.
type Controller interface {
	// GetInfo reads version, provider, and last synchronization information.
	GetInfo(ctx context.Context) (Info, error)
	// Sync triggers the normal cloud-secrets synchronization flow.
	Sync(ctx context.Context) (SyncResult, error)
	// Close releases client resources.
	Close() error
}

type controllerAPI interface {
	// GetInfo invokes the cloud-secrets controller GetInfo RPC.
	GetInfo(
		ctx context.Context,
		in *cloudsecretspb.GetInfoRequest,
		opts ...grpc.CallOption,
	) (*cloudsecretspb.GetInfoResponse, error)
	// Sync invokes the cloud-secrets controller Sync RPC.
	Sync(
		ctx context.Context,
		in *cloudsecretspb.SyncRequest,
		opts ...grpc.CallOption,
	) (*cloudsecretspb.SyncResponse, error)
}

// Client is a gRPC cloud-secrets controller client.
type Client struct {
	api        controllerAPI
	connection *grpc.ClientConn
}

// NewClient creates a cloud-secrets controller client for address.
func NewClient(address string) (Controller, error) {
	connection, err := grpc.NewClient(address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, err
	}

	return &Client{
		api:        cloudsecretspb.NewControllerClient(connection),
		connection: connection,
	}, nil
}

// GetInfo reads cloud-secrets runtime information.
func (c *Client) GetInfo(ctx context.Context) (Info, error) {
	requestContext, cancel := context.WithTimeout(ctx, getInfoTimeout)
	defer cancel()

	response, err := c.api.GetInfo(requestContext, &cloudsecretspb.GetInfoRequest{})
	if err != nil {
		return Info{}, err
	}

	info := Info{Version: response.GetVersion()}
	if provider := response.GetProvider(); provider != nil {
		info.ProviderName = provider.GetName()
		info.ProviderLink = provider.GetLink()
	}
	if timestamp := response.GetLastSyncAt(); timestamp != nil && timestamp.IsValid() {
		lastSyncAt := timestamp.AsTime()
		info.LastSyncAt = &lastSyncAt
	}
	if timestamp := response.GetNextSyncAt(); timestamp != nil && timestamp.IsValid() {
		nextSyncAt := timestamp.AsTime()
		info.NextSyncAt = &nextSyncAt
	}

	return info, nil
}

// Sync triggers cloud-secrets synchronization.
func (c *Client) Sync(ctx context.Context) (SyncResult, error) {
	requestContext, cancel := context.WithTimeout(ctx, syncTimeout)
	defer cancel()

	response, err := c.api.Sync(requestContext, &cloudsecretspb.SyncRequest{})
	if err != nil {
		return SyncResult{}, err
	}

	return SyncResult{
		Created:   response.GetCreated(),
		Updated:   response.GetUpdated(),
		Removed:   response.GetRemoved(),
		Unchanged: response.GetUnchanged(),
	}, nil
}

// Close releases the underlying gRPC connection.
func (c *Client) Close() error {
	return c.connection.Close()
}
