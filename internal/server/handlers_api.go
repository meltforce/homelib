package server

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/meltforce/homelib/internal/model"
)

func (s *Server) writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		s.log.Error("write json", "error", err)
	}
}

func (s *Server) handleAPIHosts(w http.ResponseWriter, r *http.Request) {
	filter := model.HostFilter{
		Source:   r.URL.Query().Get("source"),
		Zone:     r.URL.Query().Get("zone"),
		Status:   r.URL.Query().Get("status"),
		HostType: r.URL.Query().Get("type"),
		Search:   r.URL.Query().Get("q"),
	}

	hosts, err := s.store.GetHosts(filter)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.writeJSON(w, hosts)
}

func (s *Server) handleAPIHost(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	host, err := s.store.GetHost(name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if host == nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	services, _ := s.store.GetServices(name, "")

	s.writeJSON(w, map[string]any{
		"host":     host,
		"services": services,
	})
}

func (s *Server) handleAPIServices(w http.ResponseWriter, r *http.Request) {
	host := r.URL.Query().Get("host")
	stack := r.URL.Query().Get("stack")

	services, err := s.store.GetServices(host, stack)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.writeJSON(w, services)
}

func (s *Server) handleAPINetworks(w http.ResponseWriter, r *http.Request) {
	networks, err := s.store.GetNetworks()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.writeJSON(w, networks)
}

func (s *Server) handleAPIFindings(w http.ResponseWriter, r *http.Request) {
	source := r.URL.Query().Get("source")
	severity := r.URL.Query().Get("severity")

	findings, err := s.store.GetFindings(source, severity)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.writeJSON(w, findings)
}

func (s *Server) handleAPISummary(w http.ResponseWriter, r *http.Request) {
	summary, err := s.store.GetSummary()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.writeJSON(w, summary)
}

func (s *Server) handleAPICollections(w http.ResponseWriter, r *http.Request) {
	runs, err := s.store.ListRuns(20)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.writeJSON(w, runs)
}

func (s *Server) handleAPITrigger(w http.ResponseWriter, r *http.Request) {
	go func() {
		if err := s.orchestrator.Run(context.Background()); err != nil {
			s.log.Error("triggered collection failed", "error", err)
		}
	}()

	s.writeJSON(w, map[string]string{"status": "started"})
}
