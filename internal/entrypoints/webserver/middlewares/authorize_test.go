package middlewares

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swarm-deploy/swarm-deploy/internal/entrypoints/webserver/authenticator"
	"github.com/swarm-deploy/swarm-deploy/internal/event/dispatcher"
	"github.com/swarm-deploy/swarm-deploy/internal/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/security"
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

type captureDispatcher struct {
	dispatched []events.Event
}

func (c *captureDispatcher) Dispatch(_ context.Context, event events.Event) {
	c.dispatched = append(c.dispatched, event)
}

func (c *captureDispatcher) Subscribe(events.Type, dispatcher.Subscriber) {}

func (*captureDispatcher) Shutdown(context.Context) error {
	return nil
}

var _ authenticator.Authenticator = (*fakeAuthenticator)(nil)
var _ dispatcher.Dispatcher = (*captureDispatcher)(nil)

func TestAuthorizeDispatchesUserAuthenticatedEventOnSessionStart(t *testing.T) {
	auth := &fakeAuthenticator{authenticateResult: true}
	eventsCapture := &captureDispatcher{}
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
	require.Len(t, eventsCapture.dispatched, 1, "expected one event")
	require.Len(t, rec.Result().Cookies(), 1, "expected session cookie to be set")
	assert.Equal(t, authSessionCookieName, rec.Result().Cookies()[0].Name, "expected session cookie name")

	dispatchedEvent, ok := eventsCapture.dispatched[0].(*events.UserAuthenticated)
	require.True(t, ok, "expected user authenticated event")
	assert.Equal(t, "admin", dispatchedEvent.Username, "expected username from basic auth")
}

func TestAuthorizeSkipsDispatchInActiveSession(t *testing.T) {
	auth := &fakeAuthenticator{authenticateResult: true}
	eventsCapture := &captureDispatcher{}
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
	assert.Len(t, eventsCapture.dispatched, 0, "expected no event for active session")
	assert.Len(t, rec.Result().Cookies(), 0, "expected no extra cookies")
}

func TestAuthorizeChallengesWhenAuthenticationFailed(t *testing.T) {
	auth := &fakeAuthenticator{authenticateResult: false}
	eventsCapture := &captureDispatcher{}
	nextCalled := false
	next := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		nextCalled = true
	})

	handler := Authorize(next, auth, eventsCapture)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	assert.False(t, nextCalled, "expected next handler to stay untouched")
	assert.True(t, auth.challenged, "expected authentication challenge")
	assert.Len(t, eventsCapture.dispatched, 0, "expected no dispatched events")
}
