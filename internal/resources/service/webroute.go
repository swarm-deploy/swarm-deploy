package service

import (
	"log/slog"

	"github.com/swarm-deploy/swarm-deploy/internal/compose"
	"github.com/swarm-deploy/webroute"
)

type WebRouteResolver struct {
	providers []webroute.Provider
}

type webroutableService struct {
	environment map[string]string
}

func NewWebRouteResolver() *WebRouteResolver {
	return &WebRouteResolver{
		providers: webroute.Providers(),
	}
}

func (s *webroutableService) Environment() (map[string]string, error) {
	return s.environment, nil
}

// Resolve resolves all routes from container env vars.
func (r *WebRouteResolver) Resolve(containerEnv []string) []webroute.Route {
	env, err := compose.NewEnvironment(containerEnv)
	if err != nil || len(env.Keys) == 0 {
		return nil
	}

	out := make([]webroute.Route, 0)
	seen := map[string]struct{}{}

	for _, provider := range r.providers {
		prRoutes, rerr := provider.Resolve(&webroutableService{
			environment: env.Map,
		})
		if rerr != nil {
			slog.Info("[service] failed to resolve web routes", slog.Any("err", rerr))
		}

		for _, route := range prRoutes {
			key := route.Domain + "-" + route.Address + "-" + route.Port
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, route)
		}
	}

	return out
}
