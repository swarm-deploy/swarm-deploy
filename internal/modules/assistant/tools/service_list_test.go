package tools

import (
	"context"
	"encoding/json/v2"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/config"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/assistant/tools/routing"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/resources/service/enrichment/metadata"
	servicemodel "github.com/swarm-deploy/swarm-deploy/internal/modules/resources/service/model"
	servicetype "github.com/swarm-deploy/swarm-deploy/internal/modules/resources/service/stype"
	webroute "github.com/swarm-deploy/webroute/api"
	"go.uber.org/mock/gomock"
)

func TestServiceListReturnsCompactSortedPage(t *testing.T) {
	t.Parallel()

	services := []servicemodel.Info{
		{
			Metadata: metadata.Metadata{Description: "Dashboards", Type: servicetype.Monitoring},
			Stack:    "monitoring",
			Name:     "grafana",
			WebRoutes: []webroute.WebRoute{
				{From: webroute.Address{Domain: "grafana.example.com", Address: "grafana.example.com", Port: "443"}},
			},
		},
		{
			Metadata: metadata.Metadata{Description: "Public API", Type: servicetype.Application},
			Stack:    "core",
			Name:     "api",
		},
		{
			Metadata: metadata.Metadata{Description: "Metrics", Type: servicetype.Monitoring},
			Stack:    "monitoring",
			Name:     "prometheus",
		},
	}

	testCases := []struct {
		name           string
		request        serviceListRequest
		expected       []serviceListItem
		expectedOffset int
		hasMore        bool
		nextOffset     *int
	}{
		{
			name: "all services are sorted",
			expected: []serviceListItem{
				{Stack: "core", Name: "api", Type: "application", Description: "Public API"},
				{
					Stack: "monitoring", Name: "grafana", Type: "monitoring", Description: "Dashboards",
					WebRoutes: []webroute.WebRoute{
						{From: webroute.Address{Domain: "grafana.example.com", Address: "grafana.example.com", Port: "443"}},
					},
				},
				{Stack: "monitoring", Name: "prometheus", Type: "monitoring", Description: "Metrics"},
			},
		},
		{
			name: "stack filter and pagination",
			request: serviceListRequest{
				Stack:  " monitoring ",
				Limit:  intPointer(1),
				Offset: 1,
			},
			expected: []serviceListItem{
				{Stack: "monitoring", Name: "prometheus", Type: "monitoring", Description: "Metrics"},
			},
			expectedOffset: 1,
		},
		{
			name: "first page reports continuation",
			request: serviceListRequest{
				Limit: intPointer(1),
			},
			expected: []serviceListItem{
				{Stack: "core", Name: "api", Type: "application", Description: "Public API"},
			},
			hasMore:    true,
			nextOffset: intPointer(1),
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			ctrl := gomock.NewController(t)
			reader := NewMockServicesReader(ctrl)
			reader.EXPECT().ReadAll(gomock.Any()).Return(services, nil)
			tool := NewServiceList(reader)

			response, err := tool.Execute(context.Background(), routing.Request{Payload: testCase.request})
			require.NoError(t, err)

			var payload struct {
				Services   []serviceListItem `json:"services"`
				Count      int               `json:"count"`
				Offset     int               `json:"offset"`
				HasMore    bool              `json:"has_more"`
				NextOffset *int              `json:"next_offset"`
			}
			encoded, err := json.Marshal(response.Payload)
			require.NoError(t, err)
			require.NoError(t, json.Unmarshal(encoded, &payload))

			assert.Equal(t, testCase.expected, payload.Services)
			assert.Equal(t, len(testCase.expected), payload.Count)
			assert.Equal(t, testCase.expectedOffset, payload.Offset)
			assert.Equal(t, testCase.hasMore, payload.HasMore)
			assert.Equal(t, testCase.nextOffset, payload.NextOffset)
		})
	}
}

func TestServiceListRejectsInvalidPagination(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name    string
		request serviceListRequest
		message string
	}{
		{name: "zero limit", request: serviceListRequest{Limit: intPointer(0)}, message: "limit must be > 0"},
		{name: "excessive limit", request: serviceListRequest{Limit: intPointer(maxServiceListLimit + 1)}, message: "limit must be <= 100"},
		{name: "negative offset", request: serviceListRequest{Offset: -1}, message: "offset must be >= 0"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			ctrl := gomock.NewController(t)
			tool := NewServiceList(NewMockServicesReader(ctrl))
			_, err := tool.Execute(context.Background(), routing.Request{Payload: testCase.request})
			require.Error(t, err)
			assert.Contains(t, err.Error(), testCase.message)
		})
	}
}

func TestServiceListDefinition(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	definition := NewServiceList(NewMockServicesReader(ctrl)).Definition()

	assert.Equal(t, config.AssistantToolNameServiceList, definition.Name)
	assert.False(t, definition.Writable)
	assert.False(t, definition.Terminal)
}
