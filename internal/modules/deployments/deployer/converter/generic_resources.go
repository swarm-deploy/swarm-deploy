package converter

import (
	"fmt"

	dockerswarm "github.com/docker/docker/api/types/swarm"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
)

func genericResources(source []compose.GenericResource) ([]dockerswarm.GenericResource, error) {
	var result []dockerswarm.GenericResource
	for _, resource := range source {
		if err := unsupported("generic_resources", resource.Extra); err != nil {
			return nil, err
		}
		discrete := resource.DiscreteResourceSpec
		if discrete == nil || discrete.Kind == "" || discrete.Value < 0 {
			return nil, fmt.Errorf("generic_resources: expected nonnegative discrete resource reservation")
		}
		if err := unsupported("discrete_resource_spec", discrete.Extra); err != nil {
			return nil, err
		}
		result = append(result, dockerswarm.GenericResource{
			DiscreteResourceSpec: &dockerswarm.DiscreteGenericResource{Kind: discrete.Kind, Value: discrete.Value},
		})
	}
	return result, nil
}
