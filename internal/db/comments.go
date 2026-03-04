package db

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"
)

func (d *DB) AddComment(ctx context.Context, projectID, taskID, author, body string) (*Comment, error) {
	now := time.Now().UTC()
	c := &Comment{
		ID:        uuid.New().String(),
		ProjectID: projectID,
		TaskID:    taskID,
		Author:    author,
		Body:      body,
		CreatedAt: now,
	}

	_, err := d.conn.ExecContext(ctx,
		`INSERT INTO comments (id, project_id, task_id, author, body, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		c.ID, c.ProjectID, c.TaskID, c.Author, c.Body, c.CreatedAt.Format(time.RFC3339))
	if err != nil {
		return nil, fmt.Errorf("adding comment: %w", err)
	}
	return c, nil
}

func (d *DB) ListComments(ctx context.Context, projectID, taskID string) ([]Comment, error) {
	rows, err := d.conn.QueryContext(ctx,
		`SELECT id, project_id, task_id, author, body, created_at
		 FROM comments WHERE project_id = ? AND task_id = ? ORDER BY created_at`, projectID, taskID)
	if err != nil {
		return nil, fmt.Errorf("listing comments: %w", err)
	}
	defer rows.Close()

	var comments []Comment
	for rows.Next() {
		var c Comment
		var createdAt string
		if err := rows.Scan(&c.ID, &c.ProjectID, &c.TaskID, &c.Author, &c.Body, &createdAt); err != nil {
			return nil, fmt.Errorf("scanning comment: %w", err)
		}
		var parseErr error
		c.CreatedAt, parseErr = time.Parse(time.RFC3339, createdAt)
		if parseErr != nil {
			log.Printf("warning: invalid created_at for comment %s: %v", c.ID, parseErr)
		}
		comments = append(comments, c)
	}
	return comments, rows.Err()
}
