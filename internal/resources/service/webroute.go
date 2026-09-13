package service

import (
	"bytes"
	"context"
	"io"
	"log/slog"

	"github.com/swarm-deploy/webroute"
)

type WebRouteResolver struct {
	providers []webroute.Provider
}

type webroutableService struct {
	environment map[string]string
	configs     []webroute.ServiceConfig
}

func NewWebRouteResolver() *WebRouteResolver {
	return &WebRouteResolver{
		providers: webroute.Providers(),
	}
}

func (s *webroutableService) Environment() (map[string]string, error) {
	return s.environment, nil
}

func (s *webroutableService) Configs() []webroute.ServiceConfig {
	configs := make([]webroute.ServiceConfig, 0, len(s.configs))
	for idx := range s.configs {
		configs = append(configs, s.configs[idx])
	}

	return configs
}

// Resolve resolves all routes from container environment and configs.
func (r *WebRouteResolver) Resolve(ctx context.Context, environment map[string]string, configs []webroute.ServiceConfig) []webroute.Route {
	if len(environment) == 0 && len(configs) == 0 {
		return nil
	}

	out := make([]webroute.Route, 0)
	seen := map[string]struct{}{}
	service := &webroutableService{
		environment: environment,
		configs:     configs,
	}

	for _, provider := range r.providers {
		prRoutes, rerr := provider.Resolve(ctx, service)
		if rerr != nil {
			slog.InfoContext(ctx, "[service] failed to resolve web routes", slog.Any("err", rerr))
		}

		for _, route := range prRoutes {
			key := string(route.Provider) + "-" + route.From.Domain + "-" + route.From.Address + "-" + route.From.Port
			if route.To != nil {
				key += "-" + route.To.Domain + "-" + route.To.Address + "-" + route.To.Port
			}
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, route)
		}
	}

	return out
}

type webrouteConfig struct {
	path string
	data []byte
}

func newWebRouteConfig(path string, data []byte) webrouteConfig {
	return webrouteConfig{
		path: path,
		data: data,
	}
}

func (c webrouteConfig) Path() string {
	return c.path
}

func (c webrouteConfig) Read(_ context.Context, out io.Writer) error {
	_, err := io.Copy(out, bytes.NewReader(c.data))
	return err
}
