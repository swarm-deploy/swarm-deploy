package middlewares

import (
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/cappuccinotm/slogx/slogm"
	"github.com/google/uuid"
	api "github.com/swarm-deploy/swarm-deploy/internal/entrypoints/webserver/generated"
)

type Log struct {
	handler http.Handler

	router router
}

type router func(method, path string) (api.Route, bool)

func NewLog(handler http.Handler, router router) *Log {
	return &Log{
		handler: handler,
		router:  router,
	}
}

type lRespWriter struct {
	http.ResponseWriter

	statusCode int
}

func (w *lRespWriter) WriteHeader(statusCode int) {
	w.statusCode = statusCode

	w.ResponseWriter.WriteHeader(statusCode)
}

func (l *Log) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	reqID := req.Header.Get("x-request-id")
	if reqID == "" {
		reqID = uuid.NewString()
	}

	ctx := slogm.ContextWithRequestID(req.Context(), reqID)

	req = req.WithContext(ctx)

	log := slog.
		With(slog.String("req_method", req.Method)).
		With(slog.String("req_uri", req.RequestURI)).
		With(slog.String("req_handler", l.buildMethodName(req)))

	log.InfoContext(ctx, "handling request")

	writer := &lRespWriter{
		ResponseWriter: w,
		statusCode:     http.StatusOK,
	}

	startTime := time.Now().UnixNano()

	l.handler.ServeHTTP(writer, req)

	latency := time.Now().UnixNano() - startTime

	log.
		With(slog.Int("req_status", writer.statusCode)).
		With(slog.Int64("latency", latency)).
		InfoContext(req.Context(), "request handled")
}

func (l *Log) buildMethodName(req *http.Request) string {
	path := req.URL.Path

	if route, ok := l.router(req.Method, req.URL.Path); ok {
		path = route.PathPattern()
	}

	return fmt.Sprintf("%s %s", req.Method, path)
}
