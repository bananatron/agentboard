package db

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/google/uuid"
)

func (d *DB) CreateProject(ctx context.Context, slug, name string) (*Project, error) {
	slug = strings.TrimSpace(strings.ToLower(slug))
	if slug == "" {
		return nil, fmt.Errorf("project slug is required")
	}
	if name = strings.TrimSpace(name); name == "" {
		return nil, fmt.Errorf("project name is required")
	}
	now := time.Now().UTC()
	p := &Project{
		ID:        uuid.New().String(),
		Slug:      slug,
		Name:      name,
		CreatedAt: now,
		UpdatedAt: now,
	}
	_, err := d.conn.ExecContext(ctx,
		`INSERT INTO projects (id, slug, name, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`,
		p.ID, p.Slug, p.Name, p.CreatedAt.Format(time.RFC3339), p.UpdatedAt.Format(time.RFC3339))
	if err != nil {
		return nil, fmt.Errorf("creating project: %w", err)
	}
	return p, nil
}

func (d *DB) ListProjects(ctx context.Context) ([]Project, error) {
	rows, err := d.conn.QueryContext(ctx,
		`SELECT id, slug, name, created_at, updated_at FROM projects ORDER BY created_at`)
	if err != nil {
		return nil, fmt.Errorf("listing projects: %w", err)
	}
	defer rows.Close()

	var projects []Project
	for rows.Next() {
		var p Project
		var createdAt, updatedAt string
		if err := rows.Scan(&p.ID, &p.Slug, &p.Name, &createdAt, &updatedAt); err != nil {
			return nil, fmt.Errorf("scanning project: %w", err)
		}
		var parseErr error
		p.CreatedAt, parseErr = parseProjectTime(createdAt)
		if parseErr != nil {
			log.Printf("warning: invalid created_at for project %s: %v", p.ID, parseErr)
		}
		p.UpdatedAt, parseErr = parseProjectTime(updatedAt)
		if parseErr != nil {
			log.Printf("warning: invalid updated_at for project %s: %v", p.ID, parseErr)
		}
		projects = append(projects, p)
	}
	return projects, rows.Err()
}

func (d *DB) GetProjectByID(ctx context.Context, id string) (*Project, error) {
	row := d.conn.QueryRowContext(ctx,
		`SELECT id, slug, name, created_at, updated_at FROM projects WHERE id=?`, id)
	return scanProject(row)
}

func (d *DB) GetProjectBySlug(ctx context.Context, slug string) (*Project, error) {
	row := d.conn.QueryRowContext(ctx,
		`SELECT id, slug, name, created_at, updated_at FROM projects WHERE slug=?`, strings.ToLower(slug))
	return scanProject(row)
}

func (d *DB) RenameProject(ctx context.Context, id, newName string) (*Project, error) {
	if strings.TrimSpace(newName) == "" {
		return nil, fmt.Errorf("project name is required")
	}
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := d.conn.ExecContext(ctx,
		`UPDATE projects SET name=?, updated_at=? WHERE id=?`, newName, now, id)
	if err != nil {
		return nil, fmt.Errorf("renaming project: %w", err)
	}
	affected, err := res.RowsAffected()
	if err == nil && affected == 0 {
		return nil, sql.ErrNoRows
	}
	return d.GetProjectByID(ctx, id)
}

func scanProject(row *sql.Row) (*Project, error) {
	var p Project
	var createdAt, updatedAt string
	if err := row.Scan(&p.ID, &p.Slug, &p.Name, &createdAt, &updatedAt); err != nil {
		return nil, err
	}
	var parseErr error
	p.CreatedAt, parseErr = parseProjectTime(createdAt)
	if parseErr != nil {
		log.Printf("warning: invalid created_at for project %s: %v", p.ID, parseErr)
	}
	p.UpdatedAt, parseErr = parseProjectTime(updatedAt)
	if parseErr != nil {
		log.Printf("warning: invalid updated_at for project %s: %v", p.ID, parseErr)
	}
	return &p, nil
}

func parseProjectTime(value string) (time.Time, error) {
	layouts := []string{time.RFC3339, "2006-01-02 15:04:05"}
	for _, layout := range layouts {
		if ts, err := time.Parse(layout, value); err == nil {
			return ts, nil
		}
	}
	return time.Time{}, fmt.Errorf("unsupported timestamp %q", value)
}
