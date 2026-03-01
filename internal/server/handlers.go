package server

import (
	"context"
	"net/http"

	"github.com/meltforce/homelib/internal/model"
)

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	summary, err := s.store.GetSummary()
	if err != nil {
		s.log.Error("get summary", "error", err)
		summary = &model.Summary{
			HostsBySource: make(map[string]int),
			HostsByZone:   make(map[string]int),
			HostsByType:   make(map[string]int),
		}
	}

	findings, _ := s.store.GetFindings("", "")
	progress := s.orchestrator.CurrentProgress()

	s.render(w, "dashboard.html", map[string]any{
		"Title":    "Dashboard",
		"Summary":  summary,
		"Findings": findings,
		"Progress": progress,
		"Active":   "dashboard",
	})
}

func (s *Server) handleHosts(w http.ResponseWriter, r *http.Request) {
	filter := model.HostFilter{
		Source:   r.URL.Query().Get("source"),
		Zone:     r.URL.Query().Get("zone"),
		Status:   r.URL.Query().Get("status"),
		HostType: r.URL.Query().Get("type"),
		Search:   r.URL.Query().Get("q"),
	}

	hosts, err := s.store.GetHosts(filter)
	if err != nil {
		s.log.Error("get hosts", "error", err)
		hosts = nil
	}

	data := map[string]any{
		"Title":  "Hosts",
		"Hosts":  hosts,
		"Filter": filter,
		"Active": "hosts",
	}

	// If htmx request, render just the table partial
	if r.Header.Get("HX-Request") == "true" {
		s.renderPartial(w, "hosts.html", "hosts_table", data)
		return
	}

	s.render(w, "hosts.html", data)
}

func (s *Server) handleHostDetail(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	host, err := s.store.GetHost(name)
	if err != nil || host == nil {
		http.NotFound(w, r)
		return
	}

	services, _ := s.store.GetServices(name, "")

	s.render(w, "host_detail.html", map[string]any{
		"Title":    host.Name,
		"Host":     host,
		"Services": services,
		"Active":   "hosts",
	})
}

func (s *Server) handleServices(w http.ResponseWriter, r *http.Request) {
	host := r.URL.Query().Get("host")
	stack := r.URL.Query().Get("stack")

	services, err := s.store.GetServices(host, stack)
	if err != nil {
		s.log.Error("get services", "error", err)
	}

	s.render(w, "services.html", map[string]any{
		"Title":    "Services",
		"Services": services,
		"Active":   "services",
	})
}

func (s *Server) handleNetworks(w http.ResponseWriter, r *http.Request) {
	networks, err := s.store.GetNetworks()
	if err != nil {
		s.log.Error("get networks", "error", err)
	}

	s.render(w, "networks.html", map[string]any{
		"Title":    "Networks",
		"Networks": networks,
		"Active":   "networks",
	})
}

func (s *Server) handleFindings(w http.ResponseWriter, r *http.Request) {
	source := r.URL.Query().Get("source")
	severity := r.URL.Query().Get("severity")

	findings, err := s.store.GetFindings(source, severity)
	if err != nil {
		s.log.Error("get findings", "error", err)
	}

	s.render(w, "findings.html", map[string]any{
		"Title":    "Findings",
		"Findings": findings,
		"Active":   "findings",
	})
}

func (s *Server) handleCollections(w http.ResponseWriter, r *http.Request) {
	runs, err := s.store.ListRuns(20)
	if err != nil {
		s.log.Error("list runs", "error", err)
	}

	progress := s.orchestrator.CurrentProgress()

	s.render(w, "collections.html", map[string]any{
		"Title":    "Collections",
		"Runs":     runs,
		"Progress": progress,
		"Active":   "collections",
	})
}

func (s *Server) handleTriggerCollection(w http.ResponseWriter, r *http.Request) {
	go func() {
		if err := s.orchestrator.Run(context.Background()); err != nil {
			s.log.Error("triggered collection failed", "error", err)
		}
	}()

	http.Redirect(w, r, "/collections", http.StatusSeeOther)
}
