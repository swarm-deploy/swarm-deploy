package middlewares

import (
	"log/slog"
	"net/http"
	"runtime/debug"
	"strings"
)

func Recovery(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if err := recover(); err != nil {
				slog.ErrorContext(r.Context(),
					"[webserver] recovered from panic",
					slog.Any("err", err),
					slog.Any("stack", stacktrace()),
				)

				w.WriteHeader(http.StatusInternalServerError)
			}
		}()

		next.ServeHTTP(w, r)
	})
}

func stacktrace() []string {
	stack := strings.ReplaceAll(string(debug.Stack()), "\t", "")
	stackRows := strings.Split(stack, "\n")

	return stackRows
}
