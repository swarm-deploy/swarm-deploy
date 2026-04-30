package handlers

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	generated "github.com/swarm-deploy/swarm-deploy/internal/entrypoints/webserver/generated"
	"github.com/swarm-deploy/swarm-deploy/internal/swarm"
	"github.com/swarm-deploy/webroute"
)

func (h *handler) Search(
	ctx context.Context,
	params generated.SearchParams,
) (*generated.SearchResponse, error) {
	query := strings.ToLower(strings.TrimSpace(params.Query))
	if query == "" {
		return &generated.SearchResponse{Results: []generated.SearchResult{}}, nil
	}

	results := make([]generated.SearchResult, 0)

	results = append(results, h.searchServicesByName(query)...)
	results = append(results, h.searchServicesByWebRoute(query)...)
	results = append(results, h.searchServicesByNetwork(query)...)

	if h.secrets != nil {
		secrets, err := h.secrets.List(ctx)
		if err != nil {
			return nil, withStatusError(http.StatusInternalServerError, fmt.Errorf("list docker secrets: %w", err))
		}
		results = append(results, searchSecretsByName(secrets, query)...)
	}

	results = append(results, h.searchStacksByName(query)...)

	return &generated.SearchResponse{Results: results}, nil
}

func (h *handler) searchServicesByName(query string) []generated.SearchResult {
	if h.services == nil {
		return nil
	}

	services := h.services.List()
	results := make([]generated.SearchResult, 0, len(services))
	for _, serviceInfo := range services {
		if !strings.Contains(strings.ToLower(serviceInfo.Name), query) {
			continue
		}

		results = append(results, generated.SearchResult{
			Kind:    generated.SearchResultKindService,
			Match:   generated.SearchResultMatchServiceName,
			Label:   serviceInfo.Name,
			Stack:   generated.NewOptString(serviceInfo.Stack),
			Service: generated.NewOptString(serviceInfo.Name),
		})
	}

	return results
}

func (h *handler) searchServicesByWebRoute(query string) []generated.SearchResult {
	if h.services == nil {
		return nil
	}

	services := h.services.List()
	results := make([]generated.SearchResult, 0, len(services))
	for _, serviceInfo := range services {
		if strings.Contains(strings.ToLower(serviceInfo.Name), query) {
			continue
		}
		if !containsWebRoute(serviceInfo.WebRoutes, query) {
			continue
		}

		results = append(results, generated.SearchResult{
			Kind:    generated.SearchResultKindService,
			Match:   generated.SearchResultMatchServiceWebRoute,
			Label:   serviceInfo.Name,
			Stack:   generated.NewOptString(serviceInfo.Stack),
			Service: generated.NewOptString(serviceInfo.Name),
		})
	}

	return results
}

func (h *handler) searchServicesByNetwork(query string) []generated.SearchResult {
	if h.services == nil {
		return nil
	}

	services := h.services.List()
	results := make([]generated.SearchResult, 0, len(services))
	for _, serviceInfo := range services {
		if strings.Contains(strings.ToLower(serviceInfo.Name), query) {
			continue
		}
		if !containsNetwork(serviceInfo.Networks, query) {
			continue
		}

		results = append(results, generated.SearchResult{
			Kind:    generated.SearchResultKindService,
			Match:   generated.SearchResultMatchServiceWebRoute,
			Label:   serviceInfo.Name,
			Stack:   generated.NewOptString(serviceInfo.Stack),
			Service: generated.NewOptString(serviceInfo.Name),
		})
	}

	return results
}

func searchSecretsByName(secrets []swarm.Secret, query string) []generated.SearchResult {
	results := make([]generated.SearchResult, 0, len(secrets))
	for _, secret := range secrets {
		if !strings.Contains(strings.ToLower(secret.Name), query) {
			continue
		}

		results = append(results, generated.SearchResult{
			Kind:       generated.SearchResultKindSecret,
			Match:      generated.SearchResultMatchSecretName,
			Label:      secret.Name,
			SecretName: generated.NewOptString(secret.Name),
		})
	}

	return results
}

func (h *handler) searchStacksByName(query string) []generated.SearchResult {
	if h.control == nil {
		return nil
	}

	stacks := h.control.ListStacks()
	results := make([]generated.SearchResult, 0, len(stacks))
	for _, stack := range stacks {
		if !strings.Contains(strings.ToLower(stack.Name), query) {
			continue
		}

		results = append(results, generated.SearchResult{
			Kind:  generated.SearchResultKindStack,
			Match: generated.SearchResultMatchStackName,
			Label: stack.Name,
			Stack: generated.NewOptString(stack.Name),
		})
	}

	return results
}

func containsWebRoute(routes []webroute.Route, query string) bool {
	for _, route := range routes {
		value := strings.ToLower(strings.Join([]string{route.Domain, route.Address, route.Port}, " "))
		if strings.Contains(value, query) {
			return true
		}
	}

	return false
}

func containsNetwork(networks []string, query string) bool {
	for _, network := range networks {
		if strings.Contains(strings.ToLower(network), query) {
			return true
		}
	}

	return false
}
