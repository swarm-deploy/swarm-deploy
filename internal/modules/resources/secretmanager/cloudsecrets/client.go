//go:generate go run -mod=mod go.uber.org/mock/mockgen -source=client.go -destination=mock_controller.go -package=cloudsecrets

package cloudsecrets

import (
	"context"
	"time"

	cloudsecretspb "github.com/swarm-deploy/cloud-secrets/pkg/grpc-api"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const (
	getInfoTimeout = 3 * time.Second
	createTimeout  = 30 * time.Second
	syncTimeout    = 5 * time.Minute
)

// Info describes the runtime configuration reported by cloud-secrets.
type Info struct {
	// Version is the running cloud-secrets version.
	Version string
	// ProviderName is the configured provider display name.
	ProviderName string
	// ProviderLinks contains provider documentation and management links.
	ProviderLinks Links
	// LastSyncAt is the last successful synchronization known by this process.
	LastSyncAt *time.Time
	// NextSyncAt is the next synchronization scheduled by this process.
	NextSyncAt *time.Time
}

// Links contains optional provider destinations.
type Links struct {
	// Doc points to the provider documentation.
	Doc string
	// Manager points to the provider management UI.
	Manager string
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

// CreateRequest describes one secret payload to create in the configured provider.
type CreateRequest struct {
	// Path is the provider-specific logical secret path.
	Path string
	// Value is the literal UTF-8 secret payload.
	Value []byte
	// Key is the Vault KV v2 document key and is empty for Cloud.ru.
	Key string
	// Description is optional for Cloud.ru and empty for Vault KV v2.
	Description string
}

// CreateResult describes the created secret returned by cloud-secrets.
type CreateResult struct {
	// Path is the normalized provider path.
	Path string
	// ManagerURL is the optional provider URL for the created secret.
	ManagerURL string
}

// Controller controls one cloud-secrets instance.
type Controller interface {
	// GetInfo reads version, provider, and last synchronization information.
	GetInfo(ctx context.Context) (Info, error)
	// Create creates one secret in the configured external provider without synchronizing.
	Create(ctx context.Context, req CreateRequest) (CreateResult, error)
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

type secretsAPI interface {
	// Create invokes the cloud-secrets Secrets Create RPC.
	Create(
		ctx context.Context,
		in *cloudsecretspb.CreateSecretRequest,
		opts ...grpc.CallOption,
	) (*cloudsecretspb.CreateSecretResponse, error)
}

// Client is a gRPC cloud-secrets controller client.
type Client struct {
	controller controllerAPI
	secrets    secretsAPI
	connection *grpc.ClientConn
}

// NewClient creates a cloud-secrets controller client for address.
func NewClient(address string) (Controller, error) {
	connection, err := grpc.NewClient(address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, err
	}

	return &Client{
		controller: cloudsecretspb.NewControllerClient(connection),
		secrets:    cloudsecretspb.NewSecretsClient(connection),
		connection: connection,
	}, nil
}

// GetInfo reads cloud-secrets runtime information.
func (c *Client) GetInfo(ctx context.Context) (Info, error) {
	requestContext, cancel := context.WithTimeout(ctx, getInfoTimeout)
	defer cancel()

	response, err := c.controller.GetInfo(requestContext, &cloudsecretspb.GetInfoRequest{})
	if err != nil {
		return Info{}, err
	}

	info := Info{Version: response.GetVersion()}
	if provider := response.GetProvider(); provider != nil {
		info.ProviderName = provider.GetName()
		if links := provider.GetLinks(); links != nil {
			info.ProviderLinks = Links{Doc: links.GetDoc(), Manager: links.GetManager()}
		}
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

// Create creates a secret in the external provider without triggering synchronization.
func (c *Client) Create(ctx context.Context, req CreateRequest) (CreateResult, error) {
	requestContext, cancel := context.WithTimeout(ctx, createTimeout)
	defer cancel()

	response, err := c.secrets.Create(requestContext, &cloudsecretspb.CreateSecretRequest{
		Path:        req.Path,
		Value:       req.Value,
		Key:         req.Key,
		Description: req.Description,
	})
	if err != nil {
		return CreateResult{}, err
	}

	return CreateResult{
		Path:       response.GetPath(),
		ManagerURL: response.GetManagerUrl(),
	}, nil
}

// Sync triggers cloud-secrets synchronization.
func (c *Client) Sync(ctx context.Context) (SyncResult, error) {
	requestContext, cancel := context.WithTimeout(ctx, syncTimeout)
	defer cancel()

	response, err := c.controller.Sync(requestContext, &cloudsecretspb.SyncRequest{})
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
