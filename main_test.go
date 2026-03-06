package main

import (
	"database/sql"
	"testing"
)

func newTestApp(t *testing.T) *App {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	db.SetMaxOpenConns(1)
	app := &App{db: db}
	if err := app.initDB(); err != nil {
		t.Fatalf("initDB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return app
}

func TestLoginAuditsFromToFilter(t *testing.T) {
	app := newTestApp(t)
	_, err := app.db.Exec(`
		INSERT INTO login_audits(ip,user_agent,success,reason,created_at) VALUES
		('1.1.1.1','ua',1,'ok','2026-03-01T00:00:00+08:00'),
		('2.2.2.2','ua',0,'bad_token','2026-03-05T12:00:00+08:00'),
		('3.3.3.3','ua',1,'ok','2026-03-07T23:59:59+08:00')
	`)
	if err != nil {
		t.Fatalf("seed login audits: %v", err)
	}

	q := LoginAuditQuery{Limit: 50, Offset: 0, From: "2026-03-05T00:00:00+08:00", To: "2026-03-06T23:59:59+08:00"}
	items, err := app.listLoginAudits(q)
	if err != nil {
		t.Fatalf("listLoginAudits: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if items[0].IP != "2.2.2.2" {
		t.Fatalf("expected filtered IP 2.2.2.2, got %s", items[0].IP)
	}

	total, err := app.countLoginAudits(q)
	if err != nil {
		t.Fatalf("countLoginAudits: %v", err)
	}
	if total != 1 {
		t.Fatalf("expected filtered total=1, got %d", total)
	}
}

func TestParseCCSwitchProvidersFromSQL_MultiRowAndReport(t *testing.T) {
	sqlText := `
INSERT INTO "providers" ("id","name","app_type","settings_config","notes") VALUES
('id-1','alpha','codex','{"baseUrl":"https://api.example.com/v1","apiKey":"k1","model":"gpt-5"}','ok note'),
('id-2','beta','opencode','{"base_url":"https://proxy.example.com","model_id":"claude-3"}','with,comma'),
('id-3','','gemini','{"endpoint":"https://gemini.example.com/v1"}','fallback name'),
('id-4','x','unknown','{"baseUrl":"https://x.example.com"}','skip'),
('id-5','no-base','claude','{"apiKey":"k"}','skip');
`
	items, report, err := parseCCSwitchProvidersFromSQL(sqlText)
	if err != nil {
		t.Fatalf("parseCCSwitchProvidersFromSQL: %v", err)
	}
	if len(items) != 3 {
		t.Fatalf("expected 3 importable items, got %d", len(items))
	}
	if report.TotalRows != 5 || report.ImportableRows != 3 || report.SkippedRows != 2 {
		t.Fatalf("unexpected report summary: %+v", report)
	}
	if report.TargetMapped["codex"] != 1 || report.TargetMapped["openclaw"] != 1 || report.TargetMapped["gemini"] != 1 {
		t.Fatalf("unexpected target mapping counts: %+v", report.TargetMapped)
	}
	if items[2].Name != "id-3" {
		t.Fatalf("expected fallback name=id-3, got %s", items[2].Name)
	}
}
