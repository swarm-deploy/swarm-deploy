package modelstore

import (
	"github.com/swarm-deploy/swarm-deploy/internal/modules/resources/service/enrichment/metadata"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/resources/service/model"
	serviceType "github.com/swarm-deploy/swarm-deploy/internal/modules/resources/service/stype"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/knownapp"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	webroute "github.com/swarm-deploy/webroute/api"
)

// storeInfo is the JSON part of a services row. Stack and name live only in columns.
type storeInfo struct {
	// KnownApp is a recognized application identifier.
	KnownApp knownapp.Name `json:"known_app"`
	// Description is a human-readable service description.
	Description string `json:"description"`
	// Type is a service classification.
	Type serviceType.Type `json:"type"`
	// RepositoryURL is a source repository URL resolved from service labels.
	RepositoryURL string `json:"repository_url"`
	// RepositoryProvider identifies the source repository provider when known.
	RepositoryProvider string `json:"repository_provider,omitempty"`
	// Links is a list of additional service-related links resolved from service labels.
	Links []metadata.Link `json:"links"`
	// Image is a service container image reference.
	Image string `json:"image"`
	// Environment is a resolved container environment snapshot.
	Environment map[string]string `json:"environment,omitempty"`
	// Spec is a compact persisted service spec snapshot.
	Spec swarm.ServiceSpec `json:"spec"`
	// WebRoutes is a list of public web routes resolved from service environment.
	WebRoutes []webroute.WebRoute `json:"web_routes,omitempty"`
}

func (i storeInfo) toInfo(stack, name string) model.Info {
	return model.Info{
		Metadata: metadata.Metadata{
			KnownApp:           i.KnownApp,
			Description:        i.Description,
			Type:               i.Type,
			RepositoryURL:      i.RepositoryURL,
			RepositoryProvider: i.RepositoryProvider,
			Links:              i.Links,
		},
		Name:        name,
		Stack:       stack,
		Image:       i.Image,
		Environment: i.Environment,
		Spec:        i.Spec,
		WebRoutes:   i.WebRoutes,
	}
}

func storeInfoFromServiceInfo(info model.Info) storeInfo {
	return storeInfo{
		KnownApp:           info.KnownApp,
		Description:        info.Description,
		Type:               info.Type,
		RepositoryURL:      info.RepositoryURL,
		RepositoryProvider: info.RepositoryProvider,
		Links:              info.Links,
		Image:              info.Image,
		Environment:        info.Environment,
		Spec:               info.Spec,
		WebRoutes:          info.WebRoutes,
	}
}
