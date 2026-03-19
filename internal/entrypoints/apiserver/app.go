package apiserver

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/artarts36/go-entrypoint"
	"github.com/artarts36/swarm-deploy/internal/controller"
)

const readHeaderTimeout = 10 * time.Second

type Application struct {
	mux     *http.ServeMux
	server  *http.Server
	control *controller.Controller
}

func NewApplication(address string, control *controller.Controller) *Application {
	app := &Application{
		mux:     http.NewServeMux(),
		control: control,
	}

	app.registerRoutes()
	app.server = &http.Server{
		Addr:              address,
		Handler:           app.mux,
		ReadHeaderTimeout: readHeaderTimeout,
	}

	return app
}

func (a *Application) Entrypoint() entrypoint.Entrypoint {
	return entrypoint.HTTPServer("api-server", a.server)
}

func (a *Application) registerRoutes() {
	a.mux.HandleFunc("/api/v1/stacks", a.handleListStacks)
	a.mux.HandleFunc("/api/v1/sync", a.handleManualSync)
}

func (a *Application) handleListStacks(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"stacks": a.control.ListStacks(),
		"sync":   a.control.LastSyncInfo(),
	})
}

func (a *Application) handleManualSync(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	queued := a.control.Trigger(controller.TriggerManual)
	writeJSON(w, http.StatusAccepted, map[string]any{
		"queued": queued,
	})
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
