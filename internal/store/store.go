package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"time"

	"github.com/meltforce/homelib/internal/model"
	_ "modernc.org/sqlite"
)

// Store provides SQLite persistence for inventory data.
type Store struct {
	db *sql.DB
}

// New opens (or creates) the SQLite database and applies the schema.
func New(dataDir string) (*Store, error) {
	dbPath := filepath.Join(dataDir, "homelib.db")
	db, err := sql.Open("sqlite", dbPath+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}

	if _, err := db.Exec(schemaSQL); err != nil {
		db.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}

	return &Store{db: db}, nil
}

// Close closes the database.
func (s *Store) Close() error {
	return s.db.Close()
}

// --- Collection Runs ---

// StartRun creates a new collection run and returns its ID.
func (s *Store) StartRun() (int64, error) {
	res, err := s.db.Exec(
		"INSERT INTO collection_runs (started_at, status) VALUES (?, 'running')",
		time.Now().UTC().Format(time.RFC3339),
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// FinishRun marks a run as completed or failed.
func (s *Store) FinishRun(runID int64, status string, summary any) error {
	now := time.Now().UTC()
	var summaryJSON []byte
	if summary != nil {
		var err error
		summaryJSON, err = json.Marshal(summary)
		if err != nil {
			return err
		}
	}
	_, err := s.db.Exec(`
		UPDATE collection_runs
		SET finished_at = ?, status = ?, duration_ms = (
			CAST((julianday(?) - julianday(started_at)) * 86400000 AS INTEGER)
		), summary = ?
		WHERE id = ?`,
		now.Format(time.RFC3339), status, now.Format(time.RFC3339),
		nullString(summaryJSON), runID,
	)
	return err
}

// GetLatestRun returns the most recent collection run.
func (s *Store) GetLatestRun() (*model.CollectionRun, error) {
	row := s.db.QueryRow(`
		SELECT id, started_at, finished_at, status, duration_ms, summary
		FROM collection_runs ORDER BY id DESC LIMIT 1`)
	return scanRun(row)
}

// GetRun returns a specific collection run by ID.
func (s *Store) GetRun(id int64) (*model.CollectionRun, error) {
	row := s.db.QueryRow(`
		SELECT id, started_at, finished_at, status, duration_ms, summary
		FROM collection_runs WHERE id = ?`, id)
	return scanRun(row)
}

// ListRuns returns recent collection runs.
func (s *Store) ListRuns(limit int) ([]model.CollectionRun, error) {
	rows, err := s.db.Query(`
		SELECT id, started_at, finished_at, status, duration_ms, summary
		FROM collection_runs ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var runs []model.CollectionRun
	for rows.Next() {
		r, err := scanRunRow(rows)
		if err != nil {
			return nil, err
		}
		runs = append(runs, *r)
	}
	return runs, rows.Err()
}

// --- Collection Sources ---

// StartSource records the beginning of a source collection.
func (s *Store) StartSource(runID int64, source, sourceType string) (int64, error) {
	res, err := s.db.Exec(`
		INSERT INTO collection_sources (run_id, source, source_type, status, started_at)
		VALUES (?, ?, ?, 'running', ?)`,
		runID, source, sourceType, time.Now().UTC().Format(time.RFC3339),
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// FinishSource marks a source collection as completed or failed.
func (s *Store) FinishSource(id int64, status string, itemCount int, errMsg string) error {
	_, err := s.db.Exec(`
		UPDATE collection_sources
		SET status = ?, finished_at = ?, item_count = ?, error_message = ?
		WHERE id = ?`,
		status, time.Now().UTC().Format(time.RFC3339), itemCount, nullableString(errMsg), id,
	)
	return err
}

// GetRunSources returns all collection sources for a run.
func (s *Store) GetRunSources(runID int64) ([]model.CollectionSource, error) {
	rows, err := s.db.Query(`
		SELECT id, run_id, source, source_type, status, started_at, finished_at, error_message, item_count
		FROM collection_sources WHERE run_id = ? ORDER BY id`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sources []model.CollectionSource
	for rows.Next() {
		var cs model.CollectionSource
		var startedAt, finishedAt, errMsg sql.NullString
		if err := rows.Scan(&cs.ID, &cs.RunID, &cs.Source, &cs.SourceType, &cs.Status,
			&startedAt, &finishedAt, &errMsg, &cs.ItemCount); err != nil {
			return nil, err
		}
		cs.StartedAt, _ = time.Parse(time.RFC3339, startedAt.String)
		if finishedAt.Valid {
			t, _ := time.Parse(time.RFC3339, finishedAt.String)
			cs.FinishedAt = &t
		}
		cs.ErrorMessage = errMsg.String
		sources = append(sources, cs)
	}
	return sources, rows.Err()
}

// --- Hosts ---

// InsertHosts bulk-inserts hosts for a run.
func (s *Store) InsertHosts(runID int64, hosts []model.Host) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`
		INSERT INTO hosts (run_id, name, source, host_type, status, zone, tailscale_ip,
			local_ip, public_ipv4, cpu_cores, memory_mb, disk_gb, application, category,
			monthly_cost_eur, details)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, h := range hosts {
		_, err := stmt.Exec(runID, h.Name, h.Source, h.HostType, h.Status, h.Zone,
			nullableString(h.TailscaleIP), nullableString(h.LocalIP), nullableString(h.PublicIPv4),
			nullableInt(h.CPUCores), nullableInt(h.MemoryMB), nullableFloat(h.DiskGB),
			nullableString(h.Application), nullableString(h.Category),
			nullableFloat(h.MonthlyCostEUR), rawJSON(h.Details))
		if err != nil {
			return fmt.Errorf("insert host %s: %w", h.Name, err)
		}
	}
	return tx.Commit()
}

// GetHosts returns hosts from the latest run, with optional filters.
func (s *Store) GetHosts(filter model.HostFilter) ([]model.Host, error) {
	query := `SELECT id, run_id, name, source, host_type, status, zone, tailscale_ip,
		local_ip, public_ipv4, cpu_cores, memory_mb, disk_gb, application, category,
		monthly_cost_eur, details
		FROM hosts WHERE run_id = (SELECT MAX(id) FROM collection_runs WHERE status = 'completed')`

	var args []any
	if filter.Source != "" {
		query += " AND source = ?"
		args = append(args, filter.Source)
	}
	if filter.Zone != "" {
		query += " AND zone = ?"
		args = append(args, filter.Zone)
	}
	if filter.Status != "" {
		query += " AND status = ?"
		args = append(args, filter.Status)
	}
	if filter.HostType != "" {
		query += " AND host_type = ?"
		args = append(args, filter.HostType)
	}
	if filter.Search != "" {
		query += " AND (name LIKE ? OR application LIKE ? OR tailscale_ip LIKE ?)"
		s := "%" + filter.Search + "%"
		args = append(args, s, s, s)
	}
	query += " ORDER BY name"

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanHosts(rows)
}

// GetHost returns a single host by name from the latest run.
func (s *Store) GetHost(name string) (*model.Host, error) {
	row := s.db.QueryRow(`SELECT id, run_id, name, source, host_type, status, zone, tailscale_ip,
		local_ip, public_ipv4, cpu_cores, memory_mb, disk_gb, application, category,
		monthly_cost_eur, details
		FROM hosts
		WHERE run_id = (SELECT MAX(id) FROM collection_runs WHERE status = 'completed')
		AND name = ?`, name)
	return scanHost(row)
}

// --- Services ---

// InsertServices bulk-inserts services for a run.
func (s *Store) InsertServices(runID int64, services []model.Service) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`
		INSERT INTO services (run_id, host_name, source, service_name, container_name, image, stack_name, details)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, svc := range services {
		_, err := stmt.Exec(runID, svc.HostName, svc.Source, svc.ServiceName,
			nullableString(svc.ContainerName), nullableString(svc.Image),
			nullableString(svc.StackName), rawJSON(svc.Details))
		if err != nil {
			return fmt.Errorf("insert service %s: %w", svc.ServiceName, err)
		}
	}
	return tx.Commit()
}

// GetServices returns services from the latest run with optional host filter.
func (s *Store) GetServices(hostName, stackName string) ([]model.Service, error) {
	query := `SELECT id, run_id, host_name, source, service_name, container_name, image, stack_name, details
		FROM services WHERE run_id = (SELECT MAX(id) FROM collection_runs WHERE status = 'completed')`
	var args []any
	if hostName != "" {
		query += " AND host_name = ?"
		args = append(args, hostName)
	}
	if stackName != "" {
		query += " AND stack_name = ?"
		args = append(args, stackName)
	}
	query += " ORDER BY host_name, stack_name, service_name"

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var services []model.Service
	for rows.Next() {
		var svc model.Service
		var container, image, stack, details sql.NullString
		if err := rows.Scan(&svc.ID, &svc.RunID, &svc.HostName, &svc.Source, &svc.ServiceName,
			&container, &image, &stack, &details); err != nil {
			return nil, err
		}
		svc.ContainerName = container.String
		svc.Image = image.String
		svc.StackName = stack.String
		if details.Valid {
			raw := json.RawMessage(details.String)
			svc.Details = &raw
		}
		services = append(services, svc)
	}
	return services, rows.Err()
}

// --- Networks ---

// InsertNetworks bulk-inserts networks for a run.
func (s *Store) InsertNetworks(runID int64, networks []model.Network) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`
		INSERT INTO networks (run_id, name, vlan_id, subnet, gateway, dhcp_enabled, details)
		VALUES (?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, n := range networks {
		dhcp := 0
		if n.DHCPEnabled {
			dhcp = 1
		}
		_, err := stmt.Exec(runID, n.Name, nullableInt(n.VLANID), nullableString(n.Subnet),
			nullableString(n.Gateway), dhcp, rawJSON(n.Details))
		if err != nil {
			return fmt.Errorf("insert network %s: %w", n.Name, err)
		}
	}
	return tx.Commit()
}

// GetNetworks returns networks from the latest run.
func (s *Store) GetNetworks() ([]model.Network, error) {
	rows, err := s.db.Query(`SELECT id, run_id, name, vlan_id, subnet, gateway, dhcp_enabled, details
		FROM networks WHERE run_id = (SELECT MAX(id) FROM collection_runs WHERE status = 'completed')
		ORDER BY vlan_id, name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var networks []model.Network
	for rows.Next() {
		var n model.Network
		var vlan sql.NullInt64
		var subnet, gw, details sql.NullString
		var dhcp int
		if err := rows.Scan(&n.ID, &n.RunID, &n.Name, &vlan, &subnet, &gw, &dhcp, &details); err != nil {
			return nil, err
		}
		n.VLANID = int(vlan.Int64)
		n.Subnet = subnet.String
		n.Gateway = gw.String
		n.DHCPEnabled = dhcp != 0
		if details.Valid {
			raw := json.RawMessage(details.String)
			n.Details = &raw
		}
		networks = append(networks, n)
	}
	return networks, rows.Err()
}

// --- Firewalls ---

// InsertFirewalls bulk-inserts firewalls for a run.
func (s *Store) InsertFirewalls(runID int64, firewalls []model.Firewall) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`INSERT INTO firewalls (run_id, name, rules, applied_to) VALUES (?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, f := range firewalls {
		_, err := stmt.Exec(runID, f.Name, rawJSON(f.Rules), rawJSON(f.AppliedTo))
		if err != nil {
			return fmt.Errorf("insert firewall %s: %w", f.Name, err)
		}
	}
	return tx.Commit()
}

// --- Tailscale Data ---

// InsertTailscaleACL stores the ACL policy for a run.
func (s *Store) InsertTailscaleACL(runID int64, acl *model.TailscaleACL) error {
	_, err := s.db.Exec(`INSERT INTO tailscale_acl (run_id, acl_policy) VALUES (?, ?)`,
		runID, rawJSON(acl.ACLPolicy))
	return err
}

// InsertTailscaleDNS stores DNS config for a run.
func (s *Store) InsertTailscaleDNS(runID int64, dns *model.TailscaleDNS) error {
	magic := 0
	if dns.MagicDNSEnabled {
		magic = 1
	}
	_, err := s.db.Exec(`INSERT INTO tailscale_dns (run_id, nameservers, search_paths, magic_dns_enabled, split_dns)
		VALUES (?, ?, ?, ?, ?)`,
		runID, rawJSON(dns.Nameservers), rawJSON(dns.SearchPaths), magic, rawJSON(dns.SplitDNS))
	return err
}

// InsertTailscaleRoutes stores routes for a run.
func (s *Store) InsertTailscaleRoutes(runID int64, routes []model.TailscaleRoute) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`INSERT INTO tailscale_routes (run_id, device_name, advertised, enabled) VALUES (?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, r := range routes {
		if _, err := stmt.Exec(runID, r.DeviceName, rawJSON(r.Advertised), rawJSON(r.Enabled)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// InsertTailscaleKeys stores API keys for a run.
func (s *Store) InsertTailscaleKeys(runID int64, keys []model.TailscaleKey) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`INSERT INTO tailscale_keys (run_id, key_id, description, created_at, expires_at, capabilities)
		VALUES (?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, k := range keys {
		if _, err := stmt.Exec(runID, k.KeyID, k.Description,
			k.CreatedAt.Format(time.RFC3339), k.ExpiresAt.Format(time.RFC3339),
			rawJSON(k.Capabilities)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// GetTailscaleACL returns the ACL from the latest run.
func (s *Store) GetTailscaleACL() (*json.RawMessage, error) {
	var data sql.NullString
	err := s.db.QueryRow(`SELECT acl_policy FROM tailscale_acl
		WHERE run_id = (SELECT MAX(id) FROM collection_runs WHERE status = 'completed')`).Scan(&data)
	if err != nil {
		return nil, err
	}
	if !data.Valid {
		return nil, nil
	}
	raw := json.RawMessage(data.String)
	return &raw, nil
}

// GetTailscaleDNS returns DNS config from the latest run.
func (s *Store) GetTailscaleDNS() (*model.TailscaleDNS, error) {
	var dns model.TailscaleDNS
	var ns, sp, sd sql.NullString
	var magic int
	err := s.db.QueryRow(`SELECT id, run_id, nameservers, search_paths, magic_dns_enabled, split_dns
		FROM tailscale_dns WHERE run_id = (SELECT MAX(id) FROM collection_runs WHERE status = 'completed')`).
		Scan(&dns.ID, &dns.RunID, &ns, &sp, &magic, &sd)
	if err != nil {
		return nil, err
	}
	dns.MagicDNSEnabled = magic != 0
	if ns.Valid {
		raw := json.RawMessage(ns.String)
		dns.Nameservers = &raw
	}
	if sp.Valid {
		raw := json.RawMessage(sp.String)
		dns.SearchPaths = &raw
	}
	if sd.Valid {
		raw := json.RawMessage(sd.String)
		dns.SplitDNS = &raw
	}
	return &dns, nil
}

// GetTailscaleRoutes returns routes from the latest run.
func (s *Store) GetTailscaleRoutes() ([]model.TailscaleRoute, error) {
	rows, err := s.db.Query(`SELECT id, run_id, device_name, advertised, enabled
		FROM tailscale_routes WHERE run_id = (SELECT MAX(id) FROM collection_runs WHERE status = 'completed')
		ORDER BY device_name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var routes []model.TailscaleRoute
	for rows.Next() {
		var r model.TailscaleRoute
		var adv, en sql.NullString
		if err := rows.Scan(&r.ID, &r.RunID, &r.DeviceName, &adv, &en); err != nil {
			return nil, err
		}
		if adv.Valid {
			raw := json.RawMessage(adv.String)
			r.Advertised = &raw
		}
		if en.Valid {
			raw := json.RawMessage(en.String)
			r.Enabled = &raw
		}
		routes = append(routes, r)
	}
	return routes, rows.Err()
}

// --- Plugin Metrics ---

// InsertPluginMetrics stores plugin metrics for a run.
func (s *Store) InsertPluginMetrics(runID int64, pm *model.PluginMetrics) error {
	_, err := s.db.Exec(`INSERT INTO plugin_metrics (run_id, plugin_name, metrics) VALUES (?, ?, ?)`,
		runID, pm.PluginName, rawJSON(pm.Metrics))
	return err
}

// GetPluginMetrics returns metrics for a specific plugin from the latest run.
func (s *Store) GetPluginMetrics(pluginName string) (*model.PluginMetrics, error) {
	var pm model.PluginMetrics
	var metrics sql.NullString
	err := s.db.QueryRow(`SELECT id, run_id, plugin_name, metrics FROM plugin_metrics
		WHERE run_id = (SELECT MAX(id) FROM collection_runs WHERE status = 'completed')
		AND plugin_name = ?`, pluginName).Scan(&pm.ID, &pm.RunID, &pm.PluginName, &metrics)
	if err != nil {
		return nil, err
	}
	if metrics.Valid {
		raw := json.RawMessage(metrics.String)
		pm.Metrics = &raw
	}
	return &pm, nil
}

// --- Findings ---

// InsertFindings bulk-inserts findings for a run.
func (s *Store) InsertFindings(runID int64, findings []model.Finding) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`INSERT INTO findings (run_id, source, finding_type, severity, host_name, message, details)
		VALUES (?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, f := range findings {
		if _, err := stmt.Exec(runID, f.Source, f.FindingType, f.Severity,
			nullableString(f.HostName), f.Message, rawJSON(f.Details)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// GetFindings returns findings from the latest run.
func (s *Store) GetFindings(source, severity string) ([]model.Finding, error) {
	query := `SELECT id, run_id, source, finding_type, severity, host_name, message, details
		FROM findings WHERE run_id = (SELECT MAX(id) FROM collection_runs WHERE status = 'completed')`
	var args []any
	if source != "" {
		query += " AND source = ?"
		args = append(args, source)
	}
	if severity != "" {
		query += " AND severity = ?"
		args = append(args, severity)
	}
	query += " ORDER BY CASE severity WHEN 'error' THEN 0 WHEN 'warning' THEN 1 ELSE 2 END, source"

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var findings []model.Finding
	for rows.Next() {
		var f model.Finding
		var hostName, details sql.NullString
		if err := rows.Scan(&f.ID, &f.RunID, &f.Source, &f.FindingType, &f.Severity,
			&hostName, &f.Message, &details); err != nil {
			return nil, err
		}
		f.HostName = hostName.String
		if details.Valid {
			raw := json.RawMessage(details.String)
			f.Details = &raw
		}
		findings = append(findings, f)
	}
	return findings, rows.Err()
}

// --- Summary ---

// GetSummary returns a high-level inventory summary from the latest run.
func (s *Store) GetSummary() (*model.Summary, error) {
	latestRun, err := s.GetLatestRun()
	if err != nil {
		return nil, err
	}

	summary := &model.Summary{
		HostsBySource:  make(map[string]int),
		HostsByZone:    make(map[string]int),
		HostsByType:    make(map[string]int),
		LastCollection: latestRun,
	}

	if latestRun == nil {
		return summary, nil
	}
	runID := latestRun.ID

	// Total / online hosts
	s.db.QueryRow("SELECT COUNT(*) FROM hosts WHERE run_id = ?", runID).Scan(&summary.TotalHosts)
	s.db.QueryRow("SELECT COUNT(*) FROM hosts WHERE run_id = ? AND status IN ('running', 'online')", runID).Scan(&summary.OnlineHosts)
	s.db.QueryRow("SELECT COUNT(*) FROM services WHERE run_id = ?", runID).Scan(&summary.TotalServices)
	s.db.QueryRow("SELECT COUNT(*) FROM networks WHERE run_id = ?", runID).Scan(&summary.TotalNetworks)
	s.db.QueryRow("SELECT COUNT(*) FROM findings WHERE run_id = ?", runID).Scan(&summary.TotalFindings)
	s.db.QueryRow("SELECT COALESCE(SUM(monthly_cost_eur), 0) FROM hosts WHERE run_id = ?", runID).Scan(&summary.MonthlyCostEUR)

	// By source
	rows, _ := s.db.Query("SELECT source, COUNT(*) FROM hosts WHERE run_id = ? GROUP BY source", runID)
	if rows != nil {
		defer rows.Close()
		for rows.Next() {
			var src string
			var cnt int
			rows.Scan(&src, &cnt)
			summary.HostsBySource[src] = cnt
		}
	}

	// By zone
	rows2, _ := s.db.Query("SELECT COALESCE(zone, 'unknown'), COUNT(*) FROM hosts WHERE run_id = ? GROUP BY zone", runID)
	if rows2 != nil {
		defer rows2.Close()
		for rows2.Next() {
			var zone string
			var cnt int
			rows2.Scan(&zone, &cnt)
			summary.HostsByZone[zone] = cnt
		}
	}

	// By type
	rows3, _ := s.db.Query("SELECT host_type, COUNT(*) FROM hosts WHERE run_id = ? GROUP BY host_type", runID)
	if rows3 != nil {
		defer rows3.Close()
		for rows3.Next() {
			var ht string
			var cnt int
			rows3.Scan(&ht, &cnt)
			summary.HostsByType[ht] = cnt
		}
	}

	return summary, nil
}

// --- Retention ---

// PurgeOldRuns deletes runs older than retentionDays.
func (s *Store) PurgeOldRuns(retentionDays int) (int64, error) {
	cutoff := time.Now().UTC().AddDate(0, 0, -retentionDays).Format(time.RFC3339)

	// Get IDs to delete
	rows, err := s.db.Query("SELECT id FROM collection_runs WHERE started_at < ?", cutoff)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	var ids []int64
	for rows.Next() {
		var id int64
		rows.Scan(&id)
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return 0, nil
	}

	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	tables := []string{"hosts", "services", "networks", "firewalls",
		"tailscale_acl", "tailscale_dns", "tailscale_routes", "tailscale_keys",
		"plugin_metrics", "findings", "collection_sources"}

	for _, id := range ids {
		for _, tbl := range tables {
			tx.Exec("DELETE FROM "+tbl+" WHERE run_id = ?", id)
		}
		tx.Exec("DELETE FROM collection_runs WHERE id = ?", id)
	}

	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return int64(len(ids)), nil
}

// --- Search ---

// SearchInventory performs a text search across hosts, services, and findings.
func (s *Store) SearchInventory(query string) ([]model.Host, []model.Service, []model.Finding, error) {
	q := "%" + query + "%"

	hosts, err := s.GetHosts(model.HostFilter{Search: query})
	if err != nil {
		return nil, nil, nil, err
	}

	svcRows, err := s.db.Query(`SELECT id, run_id, host_name, source, service_name, container_name, image, stack_name, details
		FROM services WHERE run_id = (SELECT MAX(id) FROM collection_runs WHERE status = 'completed')
		AND (service_name LIKE ? OR container_name LIKE ? OR image LIKE ? OR stack_name LIKE ?)
		ORDER BY host_name, service_name`, q, q, q, q)
	if err != nil {
		return nil, nil, nil, err
	}
	defer svcRows.Close()
	var services []model.Service
	for svcRows.Next() {
		var svc model.Service
		var container, image, stack, details sql.NullString
		if err := svcRows.Scan(&svc.ID, &svc.RunID, &svc.HostName, &svc.Source, &svc.ServiceName,
			&container, &image, &stack, &details); err != nil {
			return nil, nil, nil, err
		}
		svc.ContainerName = container.String
		svc.Image = image.String
		svc.StackName = stack.String
		if details.Valid {
			raw := json.RawMessage(details.String)
			svc.Details = &raw
		}
		services = append(services, svc)
	}

	findRows, err := s.db.Query(`SELECT id, run_id, source, finding_type, severity, host_name, message, details
		FROM findings WHERE run_id = (SELECT MAX(id) FROM collection_runs WHERE status = 'completed')
		AND (message LIKE ? OR host_name LIKE ?)
		ORDER BY severity, source`, q, q)
	if err != nil {
		return nil, nil, nil, err
	}
	defer findRows.Close()
	var findings []model.Finding
	for findRows.Next() {
		var f model.Finding
		var hostName, details sql.NullString
		if err := findRows.Scan(&f.ID, &f.RunID, &f.Source, &f.FindingType, &f.Severity,
			&hostName, &f.Message, &details); err != nil {
			return nil, nil, nil, err
		}
		f.HostName = hostName.String
		if details.Valid {
			raw := json.RawMessage(details.String)
			f.Details = &raw
		}
		findings = append(findings, f)
	}

	return hosts, services, findings, nil
}

// --- Helpers ---

func scanRun(row *sql.Row) (*model.CollectionRun, error) {
	var r model.CollectionRun
	var startedAt string
	var finishedAt, summary sql.NullString
	var durationMs sql.NullInt64

	if err := row.Scan(&r.ID, &startedAt, &finishedAt, &r.Status, &durationMs, &summary); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	r.StartedAt, _ = time.Parse(time.RFC3339, startedAt)
	if finishedAt.Valid {
		t, _ := time.Parse(time.RFC3339, finishedAt.String)
		r.FinishedAt = &t
	}
	r.DurationMs = durationMs.Int64
	if summary.Valid {
		raw := json.RawMessage(summary.String)
		r.Summary = &raw
	}
	return &r, nil
}

type scannable interface {
	Scan(dest ...any) error
}

func scanRunRow(row scannable) (*model.CollectionRun, error) {
	var r model.CollectionRun
	var startedAt string
	var finishedAt, summary sql.NullString
	var durationMs sql.NullInt64

	if err := row.Scan(&r.ID, &startedAt, &finishedAt, &r.Status, &durationMs, &summary); err != nil {
		return nil, err
	}
	r.StartedAt, _ = time.Parse(time.RFC3339, startedAt)
	if finishedAt.Valid {
		t, _ := time.Parse(time.RFC3339, finishedAt.String)
		r.FinishedAt = &t
	}
	r.DurationMs = durationMs.Int64
	if summary.Valid {
		raw := json.RawMessage(summary.String)
		r.Summary = &raw
	}
	return &r, nil
}

func scanHosts(rows *sql.Rows) ([]model.Host, error) {
	var hosts []model.Host
	for rows.Next() {
		var h model.Host
		var zone, tsIP, localIP, pubIP, app, cat, details sql.NullString
		var cpu, mem sql.NullInt64
		var disk, cost sql.NullFloat64

		if err := rows.Scan(&h.ID, &h.RunID, &h.Name, &h.Source, &h.HostType, &h.Status,
			&zone, &tsIP, &localIP, &pubIP, &cpu, &mem, &disk, &app, &cat, &cost, &details); err != nil {
			return nil, err
		}
		h.Zone = zone.String
		h.TailscaleIP = tsIP.String
		h.LocalIP = localIP.String
		h.PublicIPv4 = pubIP.String
		h.CPUCores = int(cpu.Int64)
		h.MemoryMB = int(mem.Int64)
		h.DiskGB = disk.Float64
		h.Application = app.String
		h.Category = cat.String
		h.MonthlyCostEUR = cost.Float64
		if details.Valid {
			raw := json.RawMessage(details.String)
			h.Details = &raw
		}
		hosts = append(hosts, h)
	}
	return hosts, rows.Err()
}

func scanHost(row *sql.Row) (*model.Host, error) {
	var h model.Host
	var zone, tsIP, localIP, pubIP, app, cat, details sql.NullString
	var cpu, mem sql.NullInt64
	var disk, cost sql.NullFloat64

	if err := row.Scan(&h.ID, &h.RunID, &h.Name, &h.Source, &h.HostType, &h.Status,
		&zone, &tsIP, &localIP, &pubIP, &cpu, &mem, &disk, &app, &cat, &cost, &details); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	h.Zone = zone.String
	h.TailscaleIP = tsIP.String
	h.LocalIP = localIP.String
	h.PublicIPv4 = pubIP.String
	h.CPUCores = int(cpu.Int64)
	h.MemoryMB = int(mem.Int64)
	h.DiskGB = disk.Float64
	h.Application = app.String
	h.Category = cat.String
	h.MonthlyCostEUR = cost.Float64
	if details.Valid {
		raw := json.RawMessage(details.String)
		h.Details = &raw
	}
	return &h, nil
}

func nullableString(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}

func nullableInt(i int) sql.NullInt64 {
	if i == 0 {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: int64(i), Valid: true}
}

func nullableFloat(f float64) sql.NullFloat64 {
	if f == 0 {
		return sql.NullFloat64{}
	}
	return sql.NullFloat64{Float64: f, Valid: true}
}

func nullString(b []byte) sql.NullString {
	if b == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: string(b), Valid: true}
}

func rawJSON(rm *json.RawMessage) sql.NullString {
	if rm == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: string(*rm), Valid: true}
}
