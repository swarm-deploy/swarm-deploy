package service

import (
	"log/slog"
	"strings"

	"github.com/swarm-deploy/webroute"
)

const envSplitParts = 2

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
	env := parseContainerEnv(containerEnv)
	if len(env) == 0 {
		return nil
	}

	out := make([]webroute.Route, 0)
	seen := map[string]struct{}{}

	for _, provider := range r.providers {
		prRoutes, err := provider.Resolve(&webroutableService{
			environment: env,
		})
		if err != nil {
			slog.Info("[service] failed to resolve web routes", slog.Any("err", err))
		}

		for _, route := range prRoutes {
			key := route.Domain + "\x00" + route.Address + "\x00" + route.Port
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, route)
		}
	}

	if len(out) == 0 {
		return nil
	}

	return out
}

func parseContainerEnv(containerEnv []string) map[string]string {
	if len(containerEnv) == 0 {
		return nil
	}

	env := make(map[string]string, len(containerEnv))
	for _, item := range containerEnv {
		keyValue := strings.SplitN(item, "=", envSplitParts)
		if len(keyValue) != envSplitParts {
			continue
		}

		key := strings.TrimSpace(keyValue[0])
		value := strings.TrimSpace(keyValue[1])
		if key == "" {
			continue
		}

		env[key] = value
	}

	return env
}
