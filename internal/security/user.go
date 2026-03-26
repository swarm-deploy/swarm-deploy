package security

import (
	"context"
	"log/slog"

	"github.com/cappuccinotm/slogx"
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
