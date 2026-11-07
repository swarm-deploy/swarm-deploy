package converter

import (
	"fmt"

	dockerswarm "github.com/docker/docker/api/types/swarm"
	"github.com/swarm-deploy/swarm-deploy/internal/compose"
)

func genericResources(source []compose.GenericResource) ([]dockerswarm.GenericResource, error) {
	var result []dockerswarm.GenericResource
	for _, resource := range source {

		discrete := resource.DiscreteResourceSpec
		if discrete == nil || discrete.Kind == "" || discrete.Value < 0 {
			return nil, fmt.Errorf("generic_resources: expected nonnegative discrete resource reservation")
		}

		result = append(result, dockerswarm.GenericResource{
			DiscreteResourceSpec: &dockerswarm.DiscreteGenericResource{Kind: discrete.Kind, Value: discrete.Value},
		})
	}
	return result, nil
}
