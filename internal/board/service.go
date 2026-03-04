package board

import (
	"context"

	"github.com/markx3/agentboard/internal/db"
)

// Service defines all task operations.
type Service interface {
	ListProjects(ctx context.Context) ([]db.Project, error)
	CreateProject(ctx context.Context, slug, name string) (*db.Project, error)
	GetProjectByID(ctx context.Context, id string) (*db.Project, error)
	GetProjectBySlug(ctx context.Context, slug string) (*db.Project, error)
	RenameProject(ctx context.Context, id, name string) (*db.Project, error)

	ListTasks(ctx context.Context, projectID string) ([]db.Task, error)
	ListTasksByStatus(ctx context.Context, projectID string, status db.TaskStatus) ([]db.Task, error)
	GetTask(ctx context.Context, projectID, id string) (*db.Task, error)
	CreateTask(ctx context.Context, projectID, title, description string) (*db.Task, error)
	UpdateTask(ctx context.Context, projectID string, task *db.Task) error
	UpdateTaskFields(ctx context.Context, projectID, id string, fields db.TaskFieldUpdate) error
	MoveTask(ctx context.Context, projectID, id string, newStatus db.TaskStatus) error
	DeleteTask(ctx context.Context, projectID, id string) error
	ClaimTask(ctx context.Context, projectID, id, assignee string) error
	UnclaimTask(ctx context.Context, projectID, id string) error
	UpdateAgentActivity(ctx context.Context, projectID, id, activity string) error

	// Comments
	AddComment(ctx context.Context, projectID, taskID, author, body string) (*db.Comment, error)
	ListComments(ctx context.Context, projectID, taskID string) ([]db.Comment, error)

	// Dependencies
	AddDependency(ctx context.Context, projectID, taskID, dependsOn string) error
	RemoveDependency(ctx context.Context, projectID, taskID, dependsOn string) error
	ListDependencies(ctx context.Context, projectID, taskID string) ([]string, error)
	ListAllDependencies(ctx context.Context, projectID string) (map[string][]string, error)

	// Suggestions
	CreateSuggestion(ctx context.Context, projectID, taskID string, sugType db.SuggestionType, author, title, message string) (*db.Suggestion, error)
	GetSuggestion(ctx context.Context, projectID, id string) (*db.Suggestion, error)
	ListPendingSuggestions(ctx context.Context, projectID string) ([]db.Suggestion, error)
	ListSuggestions(ctx context.Context, projectID string, status db.SuggestionStatus) ([]db.Suggestion, error)
	AcceptSuggestion(ctx context.Context, projectID, id string) error
	DismissSuggestion(ctx context.Context, projectID, id string) error
}
