package swarm

import "github.com/swarm-deploy/swarm-deploy/internal/shared/httpx"

func matchAPIErrors(source <-chan error) <-chan error {
	matched := make(chan error, 1)

	go func() {
		defer close(matched)

		for err := range source {
			matched <- httpx.MatchError(err)
		}
	}()

	return matched
}
