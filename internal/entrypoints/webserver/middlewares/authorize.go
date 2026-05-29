package middlewares

import (
	"log/slog"
	"net/http"

	"github.com/swarm-deploy/swarm-deploy/internal/entrypoints/webserver/authenticator"
	"github.com/swarm-deploy/swarm-deploy/internal/event/dispatcher"
	"github.com/swarm-deploy/swarm-deploy/internal/event/events"
	"github.com/swarm-deploy/swarm-deploy/internal/security"
)

const (
	authSessionCookieName  = "swarm_deploy_auth_session"
	authSessionCookieValue = "1"
)

func Authorize(
	next http.Handler,
	auth authenticator.Authenticator,
	eventDispatcher dispatcher.Dispatcher,
) http.Handler {
	if auth == nil {
		return next
	}

	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		slog.InfoContext(req.Context(), "[security] authorizing request")

		user, authenticated := auth.Authenticate(req)
		if authenticated {
			req = req.WithContext(security.ContextWithUser(req.Context(), user))

			slog.InfoContext(req.Context(), "[security] request authorized")
			if !hasActiveAuthSession(req) {
				eventDispatcher.Dispatch(req.Context(), &events.UserAuthenticated{Username: user.Name})
				setActiveAuthSession(w)
			}

			next.ServeHTTP(w, req)
			return
		}

		slog.InfoContext(req.Context(), "[security] request rejected")
		auth.Challenge(w)
	})
}

func hasActiveAuthSession(r *http.Request) bool {
	cookie, err := r.Cookie(authSessionCookieName)
	if err != nil {
		return false
	}

	return cookie.Value == authSessionCookieValue
}

func setActiveAuthSession(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     authSessionCookieName,
		Value:    authSessionCookieValue,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}
