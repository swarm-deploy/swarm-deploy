package frontendserver

import (
	"io/fs"
	"net/http"
	"time"

	"github.com/artarts36/go-entrypoint"
	"github.com/artarts36/swarm-deploy/ui"
)

const readHeaderTimeout = 10 * time.Second

type Application struct {
	mux    *http.ServeMux
	server *http.Server
}

func NewApplication(address string) (*Application, error) {
	app := &Application{
		mux: http.NewServeMux(),
	}

	if err := app.registerRoutes(); err != nil {
		return nil, err
	}

	app.server = &http.Server{
		Addr:              address,
		Handler:           app.mux,
		ReadHeaderTimeout: readHeaderTimeout,
	}

	return app, nil
}

func (a *Application) Entrypoint() entrypoint.Entrypoint {
	return entrypoint.HTTPServer("frontend-server", a.server)
}

func (a *Application) registerRoutes() error {
	uiSub, err := fs.Sub(ui.FS, "ui")
	if err != nil {
		return err
	}

	a.mux.Handle("/ui/", http.StripPrefix("/ui/", http.FileServer(http.FS(uiSub))))
	a.mux.Handle("/", http.FileServer(http.FS(uiSub)))

	return nil
}
