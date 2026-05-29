package security

import (
	"context"
	"log/slog"

	"github.com/cappuccinotm/slogx"
	"github.com/swarm-deploy/swarm-deploy/internal/event/dispatcher"
	"github.com/swarm-deploy/swarm-deploy/internal/event/events"
)

type User struct {
	Name string
}

type userCtxKey struct{}

func ContextWithUser(ctx context.Context, user User) context.Context {
	return context.WithValue(ctx, userCtxKey{}, user)
}

func UserFromContext(ctx context.Context) (User, bool) {
	user, ok := ctx.Value(userCtxKey{}).(User)
	if ok {
		return user, true
	}
	return user, false
}

func LogUser() slogx.Middleware {
	return func(next slogx.HandleFunc) slogx.HandleFunc {
		return func(ctx context.Context, rec slog.Record) error {
			if user, ok := UserFromContext(ctx); ok {
				rec.AddAttrs(slog.String("user.name", user.Name))
			}
			return next(ctx, rec)
		}
	}
}

func PropagateEvent() dispatcher.Propagator {
	return func(ctx context.Context, event events.Event) events.Event {
		eventAwareUser, ok := event.(events.AwareUser)
		if ok {
			user, uok := UserFromContext(ctx)
			if uok {
				event = eventAwareUser.WithUsername(user.Name)
			}
		}
		return event
	}
}
