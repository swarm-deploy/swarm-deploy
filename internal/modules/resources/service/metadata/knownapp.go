package metadata

import (
	"github.com/swarm-deploy/swarm-deploy/internal/shared/knownapp"
	"github.com/swarm-deploy/swarm-deploy/internal/shared/labelsdict"
)

type KnownAppResolver struct {
	hasLabels map[string]knownapp.Name
}

func NewKnownAppResolver() *KnownAppResolver {
	return &KnownAppResolver{
		hasLabels: map[string]knownapp.Name{
			labelsdict.NginxProxyID: knownapp.NginxProxy,
		},
	}
}

func (r *KnownAppResolver) Resolve(labels Labels, meta *Metadata) {
	name, ok := r.resolve(labels)
	if !ok {
		return
	}

	meta.KnownApp = name
}

func (r *KnownAppResolver) resolve(labels Labels) (knownapp.Name, bool) {
	for label, app := range r.hasLabels {
		if _, ok := labels.Service[label]; ok {
			return app, true
		}
		if _, ok := labels.Container[label]; ok {
			return app, true
		}
	}

	return knownapp.Unknown, false
}
