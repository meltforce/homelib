package server

import (
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"

	"github.com/meltforce/homelib/internal/collector"
	"github.com/meltforce/homelib/internal/store"
	"github.com/meltforce/homelib/internal/web"
)

// Server is the HTTP server for the Web UI and JSON API.
type Server struct {
	store        *store.Store
	orchestrator *collector.Orchestrator
	log          *slog.Logger
	pages        map[string]*template.Template
}

var funcMap = template.FuncMap{
	"formatMB": func(mb int) string {
		if mb >= 1024 {
			return fmt.Sprintf("%.1f GB", float64(mb)/1024)
		}
		return fmt.Sprintf("%d MB", mb)
	},
	"formatGB": func(gb float64) string {
		if gb >= 1000 {
			return fmt.Sprintf("%.1f TB", gb/1000)
		}
		return fmt.Sprintf("%.1f GB", gb)
	},
}

// New creates a new HTTP server.
func New(st *store.Store, orch *collector.Orchestrator, log *slog.Logger) (*Server, error) {
	tmplFS, err := fs.Sub(web.Templates, "templates")
	if err != nil {
		return nil, fmt.Errorf("template fs: %w", err)
	}

	// Parse layout as base, then clone + add each page separately
	layout, err := template.New("layout").Funcs(funcMap).ParseFS(tmplFS, "layout.html")
	if err != nil {
		return nil, fmt.Errorf("parse layout: %w", err)
	}

	// Also parse the hosts_table partial into layout
	layout, err = layout.ParseFS(tmplFS, "hosts_table.html")
	if err != nil {
		return nil, fmt.Errorf("parse partials: %w", err)
	}

	pageFiles := []string{
		"dashboard.html",
		"hosts.html",
		"host_detail.html",
		"services.html",
		"networks.html",
		"findings.html",
		"collections.html",
	}

	pages := make(map[string]*template.Template, len(pageFiles))
	for _, pf := range pageFiles {
		clone, err := layout.Clone()
		if err != nil {
			return nil, fmt.Errorf("clone layout for %s: %w", pf, err)
		}
		tmpl, err := clone.ParseFS(tmplFS, pf)
		if err != nil {
			return nil, fmt.Errorf("parse page %s: %w", pf, err)
		}
		pages[pf] = tmpl
	}

	return &Server{
		store:        st,
		orchestrator: orch,
		log:          log,
		pages:        pages,
	}, nil
}

// Handler returns the HTTP handler with all routes.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// Static files
	staticSub, _ := fs.Sub(web.Static, "static")
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticSub))))

	// Web UI
	mux.HandleFunc("GET /{$}", s.handleDashboard)
	mux.HandleFunc("GET /hosts", s.handleHosts)
	mux.HandleFunc("GET /hosts/{name}", s.handleHostDetail)
	mux.HandleFunc("GET /services", s.handleServices)
	mux.HandleFunc("GET /networks", s.handleNetworks)
	mux.HandleFunc("GET /findings", s.handleFindings)
	mux.HandleFunc("GET /collections", s.handleCollections)
	mux.HandleFunc("POST /collections/trigger", s.handleTriggerCollection)

	// JSON API
	mux.HandleFunc("GET /api/v1/hosts", s.handleAPIHosts)
	mux.HandleFunc("GET /api/v1/hosts/{name}", s.handleAPIHost)
	mux.HandleFunc("GET /api/v1/services", s.handleAPIServices)
	mux.HandleFunc("GET /api/v1/networks", s.handleAPINetworks)
	mux.HandleFunc("GET /api/v1/findings", s.handleAPIFindings)
	mux.HandleFunc("GET /api/v1/summary", s.handleAPISummary)
	mux.HandleFunc("GET /api/v1/collections", s.handleAPICollections)
	mux.HandleFunc("POST /api/v1/collections/trigger", s.handleAPITrigger)

	return mux
}

func (s *Server) render(w http.ResponseWriter, page string, data any) {
	tmpl, ok := s.pages[page]
	if !ok {
		s.log.Error("template not found", "page", page)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.ExecuteTemplate(w, "layout", data); err != nil {
		s.log.Error("render template", "page", page, "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}

func (s *Server) renderPartial(w http.ResponseWriter, page, name string, data any) {
	tmpl, ok := s.pages[page]
	if !ok {
		s.log.Error("template not found", "page", page)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.ExecuteTemplate(w, name, data); err != nil {
		s.log.Error("render partial", "name", name, "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}
