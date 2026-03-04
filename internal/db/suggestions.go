package db

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"
)

func (d *DB) CreateSuggestion(ctx context.Context, projectID, taskID string, sugType SuggestionType, author, title, message string) (*Suggestion, error) {
	now := time.Now().UTC()
	s := &Suggestion{
		ID:        uuid.New().String(),
		ProjectID: projectID,
		TaskID:    taskID,
		Type:      sugType,
		Author:    author,
		Title:     title,
		Message:   message,
		Status:    SuggestionPending,
		CreatedAt: now,
	}

	// Pass NULL for empty task_id to satisfy foreign key constraint.
	// Proposals don't reference an existing task.
	var taskIDParam interface{} = s.TaskID
	if s.TaskID == "" {
		taskIDParam = nil
	}

	_, err := d.conn.ExecContext(ctx,
		`INSERT INTO suggestions (id, project_id, task_id, type, author, title, message, status, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		s.ID, s.ProjectID, taskIDParam, s.Type, s.Author, s.Title, s.Message, s.Status,
		s.CreatedAt.Format(time.RFC3339))
	if err != nil {
		return nil, fmt.Errorf("creating suggestion: %w", err)
	}
	return s, nil
}

func (d *DB) ListPendingSuggestions(ctx context.Context, projectID string) ([]Suggestion, error) {
	return d.listSuggestionsByStatus(ctx, projectID, SuggestionPending)
}

func (d *DB) ListSuggestionsByTask(ctx context.Context, projectID, taskID string) ([]Suggestion, error) {
	rows, err := d.conn.QueryContext(ctx,
		`SELECT id, project_id, task_id, type, author, title, message, status, created_at
		 FROM suggestions WHERE project_id=? AND task_id=? ORDER BY created_at`, projectID, taskID)
	if err != nil {
		return nil, fmt.Errorf("listing suggestions by task: %w", err)
	}
	defer rows.Close()
	return scanSuggestions(rows)
}

func (d *DB) ListSuggestions(ctx context.Context, projectID string, status SuggestionStatus) ([]Suggestion, error) {
	return d.listSuggestionsByStatus(ctx, projectID, status)
}

func (d *DB) GetSuggestion(ctx context.Context, projectID, id string) (*Suggestion, error) {
	row := d.conn.QueryRowContext(ctx,
		`SELECT id, project_id, task_id, type, author, title, message, status, created_at
		 FROM suggestions WHERE id=? AND project_id=?`, id, projectID)

	var s Suggestion
	var createdAt string
	var taskID sql.NullString
	if err := row.Scan(&s.ID, &s.ProjectID, &taskID, &s.Type, &s.Author, &s.Title, &s.Message, &s.Status, &createdAt); err != nil {
		return nil, fmt.Errorf("getting suggestion: %w", err)
	}
	s.TaskID = taskID.String
	var parseErr error
	s.CreatedAt, parseErr = time.Parse(time.RFC3339, createdAt)
	if parseErr != nil {
		log.Printf("warning: invalid created_at for suggestion %s: %v", s.ID, parseErr)
	}
	return &s, nil
}

func (d *DB) UpdateSuggestionStatus(ctx context.Context, projectID, id string, status SuggestionStatus) error {
	_, err := d.conn.ExecContext(ctx,
		`UPDATE suggestions SET status=? WHERE id=? AND project_id=?`, status, id, projectID)
	if err != nil {
		return fmt.Errorf("updating suggestion status: %w", err)
	}
	return nil
}

func (d *DB) listSuggestionsByStatus(ctx context.Context, projectID string, status SuggestionStatus) ([]Suggestion, error) {
	rows, err := d.conn.QueryContext(ctx,
		`SELECT id, project_id, task_id, type, author, title, message, status, created_at
		 FROM suggestions WHERE project_id=? AND status=? ORDER BY created_at`, projectID, status)
	if err != nil {
		return nil, fmt.Errorf("listing suggestions: %w", err)
	}
	defer rows.Close()
	return scanSuggestions(rows)
}

func scanSuggestions(rows interface {
	Next() bool
	Scan(...interface{}) error
	Err() error
}) ([]Suggestion, error) {
	var suggestions []Suggestion
	for rows.Next() {
		var s Suggestion
		var createdAt string
		var taskID sql.NullString
		if err := rows.Scan(&s.ID, &s.ProjectID, &taskID, &s.Type, &s.Author, &s.Title, &s.Message, &s.Status, &createdAt); err != nil {
			return nil, fmt.Errorf("scanning suggestion: %w", err)
		}
		s.TaskID = taskID.String
		var parseErr error
		s.CreatedAt, parseErr = time.Parse(time.RFC3339, createdAt)
		if parseErr != nil {
			log.Printf("warning: invalid created_at for suggestion %s: %v", s.ID, parseErr)
		}
		suggestions = append(suggestions, s)
	}
	return suggestions, rows.Err()
}
