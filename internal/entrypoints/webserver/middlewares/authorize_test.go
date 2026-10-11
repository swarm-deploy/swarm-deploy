package middlewares

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/dispatcher"
	"github.com/swarm-deploy/swarm-deploy/internal/modules/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/security"
	"go.uber.org/mock/gomock"
)

type fakeAuthenticator struct {
	authenticateResult bool
	challenged         bool
}

func (f *fakeAuthenticator) Authenticate(_ *http.Request) (security.User, bool) {
	return security.User{
		Name: "admin",
	}, f.authenticateResult
}

func (f *fakeAuthenticator) Challenge(_ http.ResponseWriter) {
	f.challenged = true
}

func TestAuthorizeDispatchesUserAuthenticatedEventOnSessionStart(t *testing.T) {
	auth := &fakeAuthenticator{authenticateResult: true}
	eventsCapture := dispatcher.NewMockDispatcher(gomock.NewController(t))
	var captured []events.Event
	eventsCapture.EXPECT().Publish(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, e events.Event) error { captured = append(captured, e); return nil }).AnyTimes()
	nextCalled := false
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		nextCalled = true
		w.WriteHeader(http.StatusNoContent)
	})

	handler := Authorize(next, auth, eventsCapture)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/stacks", nil)
	req.SetBasicAuth("admin", "secret")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	require.True(t, nextCalled, "expected next handler to be called")
	require.Len(t, captured, 1, "expected one event")
	require.Len(t, rec.Result().Cookies(), 1, "expected session cookie to be set")
	assert.Equal(t, authSessionCookieName, rec.Result().Cookies()[0].Name, "expected session cookie name")

	dispatchedEvent, ok := captured[0].(*events.UserAuthenticated)
	require.True(t, ok, "expected user authenticated event")
	assert.Equal(t, "admin", dispatchedEvent.Username, "expected username from basic auth")
}

func TestAuthorizeSkipsDispatchInActiveSession(t *testing.T) {
	auth := &fakeAuthenticator{authenticateResult: true}
	eventsCapture := dispatcher.NewMockDispatcher(gomock.NewController(t))
	var captured []events.Event
	eventsCapture.EXPECT().Publish(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, e events.Event) error { captured = append(captured, e); return nil }).AnyTimes()
	nextCalled := false
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		nextCalled = true
		w.WriteHeader(http.StatusNoContent)
	})

	handler := Authorize(next, auth, eventsCapture)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/stacks", nil)
	req.SetBasicAuth("admin", "secret")
	req.AddCookie(&http.Cookie{
		Name:  authSessionCookieName,
		Value: authSessionCookieValue,
		Path:  "/",
	})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	require.True(t, nextCalled, "expected next handler to be called")
	assert.Len(t, captured, 0, "expected no event for active session")
	assert.Len(t, rec.Result().Cookies(), 0, "expected no extra cookies")
}

func TestAuthorizeChallengesWhenAuthenticationFailed(t *testing.T) {
	auth := &fakeAuthenticator{authenticateResult: false}
	eventsCapture := dispatcher.NewMockDispatcher(gomock.NewController(t))
	var captured []events.Event
	eventsCapture.EXPECT().Publish(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, e events.Event) error { captured = append(captured, e); return nil }).AnyTimes()
	nextCalled := false
	next := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		nextCalled = true
	})

	handler := Authorize(next, auth, eventsCapture)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	assert.False(t, nextCalled, "expected next handler to stay untouched")
	assert.True(t, auth.challenged, "expected authentication challenge")
	assert.Len(t, captured, 0, "expected no dispatched events")
}
