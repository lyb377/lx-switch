package main

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type DashboardMetrics struct {
	LoginStats     LoginStats     `json:"login"`
	OperationStats OperationStats `json:"operations"`
	ProviderStats  ProviderStats  `json:"providers"`
	TimeSeries     []TimePoint    `json:"timeSeries"`
	Window         string         `json:"window"`
	GeneratedAt    time.Time      `json:"generatedAt"`
}

type LoginStats struct {
	Total        int     `json:"total"`
	Success      int     `json:"success"`
	Failed       int     `json:"failed"`
	SuccessRate  float64 `json:"successRate"`
	UniqueIPs    int     `json:"uniqueIps"`
	LockedEvents int     `json:"lockedEvents"`
}

type OperationStats struct {
	Total          int            `json:"total"`
	ByAction       map[string]int `json:"byAction"`
	ActivateCount  int            `json:"activateCount"`
	RollbackCount  int            `json:"rollbackCount"`
	ImportCount    int            `json:"importCount"`
	ImportSuccess  int            `json:"importSuccess"`
	ImportFailRate float64        `json:"importFailRate"`
}

type ProviderStats struct {
	Total    int            `json:"total"`
	ByTarget map[string]int `json:"byTarget"`
	Active   int            `json:"active"`
}

type TimePoint struct {
	Timestamp string         `json:"timestamp"`
	Login     map[string]int `json:"login"`
	Operation map[string]int `json:"operation"`
}

func parseWindow(r *http.Request) (string, time.Time, time.Time) {
	window := strings.TrimSpace(r.URL.Query().Get("window"))
	if window == "" {
		window = "24h"
	}

	now := time.Now()
	var from time.Time

	switch window {
	case "24h":
		from = now.Add(-24 * time.Hour)
	case "7d":
		from = now.Add(-7 * 24 * time.Hour)
	case "30d":
		from = now.Add(-30 * 24 * time.Hour)
	default:
		window = "24h"
		from = now.Add(-24 * time.Hour)
	}

	return window, from, now
}

func (a *App) handleMetricsDashboard(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	window, from, to := parseWindow(r)

	loginStats, err := a.getLoginStats(from, to)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	opStats, err := a.getOperationStats(from, to)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	providerStats, err := a.getProviderStats()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	timeSeries, err := a.getTimeSeries(window, from, to)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	metrics := DashboardMetrics{
		LoginStats:     loginStats,
		OperationStats: opStats,
		ProviderStats:  providerStats,
		TimeSeries:     timeSeries,
		Window:         window,
		GeneratedAt:    time.Now(),
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(metrics)
}

func (a *App) getLoginStats(from, to time.Time) (LoginStats, error) {
	var stats LoginStats

	err := a.db.QueryRow(
		`SELECT COUNT(1) FROM login_audits WHERE created_at >= ? AND created_at <= ?`,
		from.Format(time.RFC3339), to.Format(time.RFC3339),
	).Scan(&stats.Total)
	if err != nil {
		return stats, err
	}

	err = a.db.QueryRow(
		`SELECT COUNT(1) FROM login_audits WHERE created_at >= ? AND created_at <= ? AND success = 1`,
		from.Format(time.RFC3339), to.Format(time.RFC3339),
	).Scan(&stats.Success)
	if err != nil {
		return stats, err
	}

	err = a.db.QueryRow(
		`SELECT COUNT(1) FROM login_audits WHERE created_at >= ? AND created_at <= ? AND success = 0`,
		from.Format(time.RFC3339), to.Format(time.RFC3339),
	).Scan(&stats.Failed)
	if err != nil {
		return stats, err
	}

	err = a.db.QueryRow(
		`SELECT COUNT(DISTINCT ip) FROM login_audits WHERE created_at >= ? AND created_at <= ?`,
		from.Format(time.RFC3339), to.Format(time.RFC3339),
	).Scan(&stats.UniqueIPs)
	if err != nil {
		return stats, err
	}

	err = a.db.QueryRow(
		`SELECT COUNT(1) FROM login_audits WHERE created_at >= ? AND created_at <= ? AND reason = 'locked'`,
		from.Format(time.RFC3339), to.Format(time.RFC3339),
	).Scan(&stats.LockedEvents)
	if err != nil {
		return stats, err
	}

	if stats.Total > 0 {
		stats.SuccessRate = float64(stats.Success) / float64(stats.Total) * 100
	}

	return stats, nil
}

func (a *App) getOperationStats(from, to time.Time) (OperationStats, error) {
	var stats OperationStats
	stats.ByAction = make(map[string]int)

	err := a.db.QueryRow(
		`SELECT COUNT(1) FROM op_audits WHERE created_at >= ? AND created_at <= ?`,
		from.Format(time.RFC3339), to.Format(time.RFC3339),
	).Scan(&stats.Total)
	if err != nil {
		return stats, err
	}

	rows, err := a.db.Query(
		`SELECT action, COUNT(1) FROM op_audits WHERE created_at >= ? AND created_at <= ? GROUP BY action`,
		from.Format(time.RFC3339), to.Format(time.RFC3339),
	)
	if err != nil {
		return stats, err
	}
	defer rows.Close()

	for rows.Next() {
		var action string
		var count int
		if err := rows.Scan(&action, &count); err != nil {
			continue
		}
		stats.ByAction[action] = count

		switch action {
		case "provider.activate":
			stats.ActivateCount = count
		case "backup.rollback":
			stats.RollbackCount = count
		case "provider.import", "provider.import.ccswitch":
			stats.ImportCount += count
		}
	}

	stats.ImportSuccess = stats.ImportCount

	return stats, nil
}

func (a *App) getProviderStats() (ProviderStats, error) {
	var stats ProviderStats
	stats.ByTarget = make(map[string]int)

	err := a.db.QueryRow(`SELECT COUNT(1) FROM providers`).Scan(&stats.Total)
	if err != nil {
		return stats, err
	}

	rows, err := a.db.Query(`SELECT target, COUNT(1) FROM providers GROUP BY target`)
	if err != nil {
		return stats, err
	}
	defer rows.Close()

	for rows.Next() {
		var target string
		var count int
		if err := rows.Scan(&target, &count); err != nil {
			continue
		}
		stats.ByTarget[target] = count
	}

	activeID, _ := a.getState("active_provider")
	if activeID != "" {
		stats.Active = 1
	}

	return stats, nil
}

func (a *App) getTimeSeries(window string, from, to time.Time) ([]TimePoint, error) {
	var points []TimePoint

	var interval time.Duration
	switch window {
	case "24h":
		interval = time.Hour
	case "7d", "30d":
		interval = 24 * time.Hour
	default:
		interval = time.Hour
	}

	current := from
	for current.Before(to) || current.Equal(to) {
		next := current.Add(interval)
		if next.After(to) {
			next = to
		}

		point := TimePoint{
			Timestamp: current.Format(time.RFC3339),
			Login:     make(map[string]int),
			Operation: make(map[string]int),
		}

		var loginTotal, loginSuccess, loginFailed int
		_ = a.db.QueryRow(
			`SELECT COUNT(1), SUM(CASE WHEN success = 1 THEN 1 ELSE 0 END), SUM(CASE WHEN success = 0 THEN 1 ELSE 0 END) FROM login_audits WHERE created_at >= ? AND created_at < ?`,
			current.Format(time.RFC3339), next.Format(time.RFC3339),
		).Scan(&loginTotal, &loginSuccess, &loginFailed)

		point.Login["total"] = loginTotal
		point.Login["success"] = loginSuccess
		point.Login["failed"] = loginFailed

		rows, err := a.db.Query(
			`SELECT action, COUNT(1) FROM op_audits WHERE created_at >= ? AND created_at < ? GROUP BY action`,
			current.Format(time.RFC3339), next.Format(time.RFC3339),
		)
		if err == nil {
			for rows.Next() {
				var action string
				var count int
				if err := rows.Scan(&action, &count); err == nil {
					point.Operation[action] = count
				}
			}
			rows.Close()
		}

		points = append(points, point)
		current = next
	}

	return points, nil
}

func (a *App) handleMetricsExport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	format := strings.ToLower(r.URL.Query().Get("format"))
	if format == "" {
		format = "json"
	}

	window, from, to := parseWindow(r)

	loginStats, err := a.getLoginStats(from, to)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	opStats, err := a.getOperationStats(from, to)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	providerStats, err := a.getProviderStats()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	data := map[string]interface{}{
		"window":      window,
		"from":        from.Format(time.RFC3339),
		"to":          to.Format(time.RFC3339),
		"generatedAt": time.Now().Format(time.RFC3339),
		"login":       loginStats,
		"operations":  opStats,
		"providers":   providerStats,
	}

	switch format {
	case "csv":
		exportMetricsCSV(w, data)
	default:
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Content-Disposition", "attachment; filename=metrics-"+time.Now().Format("20060102-150405")+".json")
		_ = json.NewEncoder(w).Encode(data)
	}
}

func exportMetricsCSV(w http.ResponseWriter, data map[string]interface{}) {
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename=metrics-"+time.Now().Format("20060102-150405")+".csv")

	_, _ = w.Write([]byte("category,metric,value\n"))

	if window, ok := data["window"].(string); ok {
		_, _ = w.Write([]byte("meta,window," + csvEsc(window) + "\n"))
	}

	if login, ok := data["login"].(LoginStats); ok {
		_, _ = w.Write([]byte("login,total," + strconv.Itoa(login.Total) + "\n"))
		_, _ = w.Write([]byte("login,success," + strconv.Itoa(login.Success) + "\n"))
		_, _ = w.Write([]byte("login,failed," + strconv.Itoa(login.Failed) + "\n"))
		_, _ = w.Write([]byte("login,successRate," + strconv.FormatFloat(login.SuccessRate, 'f', 2, 64) + "\n"))
		_, _ = w.Write([]byte("login,uniqueIps," + strconv.Itoa(login.UniqueIPs) + "\n"))
		_, _ = w.Write([]byte("login,lockedEvents," + strconv.Itoa(login.LockedEvents) + "\n"))
	}

	if ops, ok := data["operations"].(OperationStats); ok {
		_, _ = w.Write([]byte("operations,total," + strconv.Itoa(ops.Total) + "\n"))
		_, _ = w.Write([]byte("operations,activateCount," + strconv.Itoa(ops.ActivateCount) + "\n"))
		_, _ = w.Write([]byte("operations,rollbackCount," + strconv.Itoa(ops.RollbackCount) + "\n"))
		_, _ = w.Write([]byte("operations,importCount," + strconv.Itoa(ops.ImportCount) + "\n"))

		for action, count := range ops.ByAction {
			_, _ = w.Write([]byte("operations,action:" + csvEsc(action) + "," + strconv.Itoa(count) + "\n"))
		}
	}

	if prov, ok := data["providers"].(ProviderStats); ok {
		_, _ = w.Write([]byte("providers,total," + strconv.Itoa(prov.Total) + "\n"))
		_, _ = w.Write([]byte("providers,active," + strconv.Itoa(prov.Active) + "\n"))

		for target, count := range prov.ByTarget {
			_, _ = w.Write([]byte("providers,target:" + csvEsc(target) + "," + strconv.Itoa(count) + "\n"))
		}
	}
}