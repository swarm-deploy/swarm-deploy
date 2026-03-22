package handlers

import (
	"github.com/artarts36/swarm-deploy/internal/controller"
	generated "github.com/artarts36/swarm-deploy/internal/entrypoints/webserver/generated"
	"github.com/artarts36/swarm-deploy/internal/event/history"
	"github.com/artarts36/swarm-deploy/internal/service"
	"github.com/artarts36/swarm-deploy/internal/swarm"
)

type handler struct {
	generated.UnimplementedHandler
	control   *controller.Controller
	inspector *swarm.Inspector
	history   *history.Store
	services  *service.Store
}

var _ generated.Handler = (*handler)(nil)

func New(
	control *controller.Controller,
	inspector *swarm.Inspector,
	history *history.Store,
	services *service.Store,
) generated.Handler {
	return &handler{
		control:   control,
		inspector: inspector,
		history:   history,
		services:  services,
	}
}
