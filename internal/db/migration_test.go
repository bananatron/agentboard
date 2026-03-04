package db_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/markx3/agentboard/internal/db"
	_ "modernc.org/sqlite"
)

const legacySchemaSQL = `
CREATE TABLE tasks (
    id TEXT PRIMARY KEY,
    title TEXT NOT NULL,
    description TEXT DEFAULT '',
    status TEXT NOT NULL DEFAULT 'backlog',
    assignee TEXT DEFAULT '',
    branch_name TEXT DEFAULT '',
    pr_url TEXT DEFAULT '',
    pr_number INTEGER DEFAULT 0,
    agent_name TEXT DEFAULT '',
    agent_status TEXT DEFAULT 'idle',
    agent_started_at TEXT DEFAULT '',
    agent_spawned_status TEXT DEFAULT '',
    reset_requested INTEGER DEFAULT 0,
    skip_permissions INTEGER DEFAULT 0,
    enrichment_status TEXT DEFAULT '',
    enrichment_agent_name TEXT DEFAULT '',
    agent_activity TEXT DEFAULT '',
    position INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE TABLE comments (
    id TEXT PRIMARY KEY,
    task_id TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    author TEXT NOT NULL,
    body TEXT NOT NULL,
    created_at TEXT NOT NULL
);
CREATE TABLE task_dependencies (
    task_id TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    depends_on TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    PRIMARY KEY (task_id, depends_on)
);
CREATE TABLE suggestions (
    id TEXT PRIMARY KEY,
    task_id TEXT REFERENCES tasks(id) ON DELETE CASCADE,
    type TEXT NOT NULL,
    author TEXT NOT NULL DEFAULT '',
    title TEXT NOT NULL DEFAULT '',
    message TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending',
    created_at TEXT NOT NULL
);
CREATE TABLE schema_version (
    version INTEGER PRIMARY KEY,
    applied_at TEXT NOT NULL DEFAULT (datetime('now'))
);
`

func TestMigrateV7toV8(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "legacy.db")
	conn, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open raw sqlite: %v", err)
	}
	if _, err := conn.Exec(legacySchemaSQL); err != nil {
		t.Fatalf("creating legacy schema: %v", err)
	}
	if _, err := conn.Exec(`INSERT INTO schema_version (version) VALUES (7)`); err != nil {
		t.Fatalf("setting schema_version: %v", err)
	}
	if _, err := conn.Exec(`INSERT INTO tasks (id, title, created_at, updated_at) VALUES ('task-1','Legacy','2024-01-01T00:00:00Z','2024-01-01T00:00:00Z')`); err != nil {
		t.Fatalf("seeding task: %v", err)
	}
	conn.Close()

	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("opening migrated db: %v", err)
	}
	defer database.Close()

	project, err := database.GetProjectBySlug(context.Background(), "default")
	if err != nil {
		t.Fatalf("fetching default project: %v", err)
	}

	tasks, err := database.ListTasks(context.Background(), project.ID)
	if err != nil {
		t.Fatalf("listing tasks after migration: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task post-migration, got %d", len(tasks))
	}
	if tasks[0].ProjectID != project.ID {
		t.Fatalf("task not assigned to default project: %s", tasks[0].ProjectID)
	}
}
