package metadata

import (
	"strings"

	"github.com/swarm-deploy/swarm-deploy/internal/shared/labelsdict"
)

const traefikOpenAPIPathSuffix = ".loadbalancer.apiportal.path"

type LinksResolver struct {
	sources map[string]string
}

func NewLinksResolver() *LinksResolver {
	return &LinksResolver{
		sources: map[string]string{
			labelsdict.GrafanaURL: "Grafana",
			labelsdict.OpenAPIURL: "OpenAPI",
		},
	}
}

func (r *LinksResolver) Resolve(labels Labels, meta *Metadata) {
	meta.Links = append(meta.Links, r.resolveLinks(labels.Service)...)
	meta.Links = append(meta.Links, r.resolveLinks(labels.Container)...)
}

func (r *LinksResolver) resolveLinks(labels map[string]string) []Link {
	links := make([]Link, 0)

	for key, title := range r.sources {
		val := labels[key]
		if val == "" {
			continue
		}

		links = append(links, Link{
			Type: title,
			URL:  val,
		})
	}

	for key, val := range labels {
		if val == "" || !strings.HasPrefix(key, "traefik.http.services.") || !strings.HasSuffix(key, traefikOpenAPIPathSuffix) {
			continue
		}

		links = append(links, Link{
			Type: "OpenAPI",
			URL:  val,
		})
	}

	return links
}
