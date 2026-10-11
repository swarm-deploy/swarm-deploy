package modelstore

import (
	"github.com/swarm-deploy/swarm-deploy/internal/modules/resources/service/enrichment/metadata"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/resources/service/model"
	serviceType "github.com/swarm-deploy/swarm-deploy/internal/modules/resources/service/stype"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/knownapp"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	webroute "github.com/swarm-deploy/webroute/api"
)

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
	// Name is a service name inside stack.
	Name string `json:"name"`
	// Stack is a docker stack name.
	Stack string `json:"stack"`
	// Image is a service container image reference.
	Image string `json:"image"`
	// Environment is a resolved container environment snapshot.
	Environment map[string]string `json:"environment,omitempty"`
	// Spec is a compact persisted service spec snapshot.
	Spec swarm.ServiceSpec `json:"spec"`
	// WebRoutes is a list of public web routes resolved from service environment.
	WebRoutes []webroute.WebRoute `json:"web_routes,omitempty"`
}

func (i storeInfo) toInfo() model.Info {
	return model.Info{
		Metadata: metadata.Metadata{
			KnownApp:           i.KnownApp,
			Description:        i.Description,
			Type:               i.Type,
			RepositoryURL:      i.RepositoryURL,
			RepositoryProvider: i.RepositoryProvider,
			Links:              i.Links,
		},
		Name:        i.Name,
		Stack:       i.Stack,
		Image:       i.Image,
		Environment: i.Environment,
		Spec:        i.Spec,
		WebRoutes:   i.WebRoutes,
	}
}

func storeInfosFromServiceInfos(infos []model.Info) []storeInfo {
	rows := make([]storeInfo, 0, len(infos))
	for _, info := range infos {
		rows = append(rows, storeInfo{
			KnownApp:           info.KnownApp,
			Description:        info.Description,
			Type:               info.Type,
			RepositoryURL:      info.RepositoryURL,
			RepositoryProvider: info.RepositoryProvider,
			Links:              info.Links,
			Name:               info.Name,
			Stack:              info.Stack,
			Image:              info.Image,
			Environment:        info.Environment,
			Spec:               info.Spec,
			WebRoutes:          info.WebRoutes,
		})
	}
	return rows
}
