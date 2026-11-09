package web

import (
	"context"
	"net/http"

	"github.com/zytyan/keepsess/internal/core"
	"github.com/zytyan/keepsess/internal/history"
)

type Manager interface {
	InstanceID() string
	TaskList(string) ([]core.Result, error)
	Operate(context.Context, string, core.Request) (core.Result, error)
}
type Backend struct {
	Manager Manager
	Store   *history.Store
	Context context.Context
}
type Registry interface {
	Instances() ([]history.Instance, error)
	Lookup(string) (Backend, error)
}

func (s *Server) backend(r *http.Request) (Backend, error) {
	if s.registry != nil {
		return s.registry.Lookup(r.URL.Query().Get("instance"))
	}
	return Backend{Manager: s.Manager, Store: s.Store, Context: s.ctx}, nil
}
