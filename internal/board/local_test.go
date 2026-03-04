package board_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/markx3/agentboard/internal/board"
	"github.com/markx3/agentboard/internal/db"
)

func setupTestService(t *testing.T) (board.Service, string) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("opening test db: %v", err)
	}
	project, err := database.GetProjectBySlug(context.Background(), "default")
	if err != nil {
		t.Fatalf("getting default project: %v", err)
	}
	t.Cleanup(func() {
		database.Close()
		os.Remove(dbPath)
	})
	return board.NewLocalService(database), project.ID
}

func TestClaimTask(t *testing.T) {
	svc, projectID := setupTestService(t)
	ctx := context.Background()

	task, err := svc.CreateTask(ctx, projectID, "Claimable", "desc")
	if err != nil {
		t.Fatalf("creating task: %v", err)
	}

	if err := svc.ClaimTask(ctx, projectID, task.ID, "alice"); err != nil {
		t.Fatalf("claiming task: %v", err)
	}

	got, _ := svc.GetTask(ctx, projectID, task.ID)
	if got.Assignee != "alice" {
		t.Errorf("got assignee %q, want %q", got.Assignee, "alice")
	}
	if got.Status != db.StatusBrainstorm {
		t.Errorf("got status %q, want %q", got.Status, db.StatusBrainstorm)
	}
}

func TestClaimAlreadyClaimed(t *testing.T) {
	svc, projectID := setupTestService(t)
	ctx := context.Background()

	task, _ := svc.CreateTask(ctx, projectID, "Claimed", "")
	svc.ClaimTask(ctx, projectID, task.ID, "alice")

	err := svc.ClaimTask(ctx, projectID, task.ID, "bob")
	if err == nil {
		t.Error("expected error claiming already-claimed task")
	}
}

func TestUnclaimTask(t *testing.T) {
	svc, projectID := setupTestService(t)
	ctx := context.Background()

	task, _ := svc.CreateTask(ctx, projectID, "Unclaim Me", "")
	svc.ClaimTask(ctx, projectID, task.ID, "alice")

	if err := svc.UnclaimTask(ctx, projectID, task.ID); err != nil {
		t.Fatalf("unclaiming: %v", err)
	}

	got, _ := svc.GetTask(ctx, projectID, task.ID)
	if got.Assignee != "" {
		t.Errorf("got assignee %q, want empty", got.Assignee)
	}
	if got.Status != db.StatusBacklog {
		t.Errorf("got status %q, want %q", got.Status, db.StatusBacklog)
	}
}

func TestUpdateTaskFields(t *testing.T) {
	svc, projectID := setupTestService(t)
	ctx := context.Background()

	task, _ := svc.CreateTask(ctx, projectID, "Original", "desc")

	newTitle := "Updated"
	if err := svc.UpdateTaskFields(ctx, projectID, task.ID, db.TaskFieldUpdate{
		Title: &newTitle,
	}); err != nil {
		t.Fatalf("partial update: %v", err)
	}

	got, _ := svc.GetTask(ctx, projectID, task.ID)
	if got.Title != "Updated" {
		t.Errorf("title: got %q, want %q", got.Title, "Updated")
	}
	if got.Description != "desc" {
		t.Errorf("description changed: got %q", got.Description)
	}
}

func TestComments(t *testing.T) {
	svc, projectID := setupTestService(t)
	ctx := context.Background()

	task, _ := svc.CreateTask(ctx, projectID, "Comments", "")
	c, err := svc.AddComment(ctx, projectID, task.ID, "alice", "hello")
	if err != nil {
		t.Fatalf("adding comment: %v", err)
	}
	if c.Author != "alice" {
		t.Errorf("author: got %q", c.Author)
	}

	comments, _ := svc.ListComments(ctx, projectID, task.ID)
	if len(comments) != 1 {
		t.Errorf("got %d comments, want 1", len(comments))
	}
}

func TestDependencies(t *testing.T) {
	svc, projectID := setupTestService(t)
	ctx := context.Background()

	t1, _ := svc.CreateTask(ctx, projectID, "A", "")
	t2, _ := svc.CreateTask(ctx, projectID, "B", "")

	if err := svc.AddDependency(ctx, projectID, t1.ID, t2.ID); err != nil {
		t.Fatalf("add dep: %v", err)
	}

	deps, _ := svc.ListDependencies(ctx, projectID, t1.ID)
	if len(deps) != 1 || deps[0] != t2.ID {
		t.Errorf("deps: got %v, want [%s]", deps, t2.ID)
	}

	all, _ := svc.ListAllDependencies(ctx, projectID)
	if len(all[t1.ID]) != 1 {
		t.Errorf("all deps for t1: got %d", len(all[t1.ID]))
	}

	if err := svc.RemoveDependency(ctx, projectID, t1.ID, t2.ID); err != nil {
		t.Fatalf("remove dep: %v", err)
	}
	deps, _ = svc.ListDependencies(ctx, projectID, t1.ID)
	if len(deps) != 0 {
		t.Errorf("after remove: got %d deps", len(deps))
	}
}

func TestSuggestions(t *testing.T) {
	svc, projectID := setupTestService(t)
	ctx := context.Background()

	task, _ := svc.CreateTask(ctx, projectID, "Suggestions", "")

	sug, err := svc.CreateSuggestion(ctx, projectID, task.ID, db.SuggestionEnrichment, "claude", "Improve title", "Should be more specific")
	if err != nil {
		t.Fatalf("creating suggestion: %v", err)
	}

	got, _ := svc.GetSuggestion(ctx, projectID, sug.ID)
	if got.Title != "Improve title" {
		t.Errorf("title: got %q", got.Title)
	}

	pending, _ := svc.ListPendingSuggestions(ctx, projectID)
	if len(pending) != 1 {
		t.Errorf("pending: got %d, want 1", len(pending))
	}

	if err := svc.DismissSuggestion(ctx, projectID, sug.ID); err != nil {
		t.Fatalf("dismissing: %v", err)
	}

	pending, _ = svc.ListPendingSuggestions(ctx, projectID)
	if len(pending) != 0 {
		t.Errorf("after dismiss: got %d pending", len(pending))
	}
}

func TestAcceptProposal(t *testing.T) {
	svc, projectID := setupTestService(t)
	ctx := context.Background()

	task, _ := svc.CreateTask(ctx, projectID, "Source", "")

	// Create a proposal suggestion
	sug, _ := svc.CreateSuggestion(ctx, projectID, task.ID, db.SuggestionProposal, "claude", "New Feature", "Build this feature")

	// Accept the proposal — should create a new task
	if err := svc.AcceptSuggestion(ctx, projectID, sug.ID); err != nil {
		t.Fatalf("accepting proposal: %v", err)
	}

	// Suggestion should now be accepted
	got, _ := svc.GetSuggestion(ctx, projectID, sug.ID)
	if got.Status != db.SuggestionAccepted {
		t.Errorf("suggestion status: got %q, want %q", got.Status, db.SuggestionAccepted)
	}

	// A new task should exist with the proposal's title and description
	tasks, _ := svc.ListTasks(ctx, projectID)
	var found bool
	for _, tk := range tasks {
		if tk.Title == "New Feature" && tk.Description == "Build this feature" {
			found = true
			// Should have enrichment_status=pending for auto-enrichment
			if tk.EnrichmentStatus != db.EnrichmentPending {
				t.Errorf("new task enrichment_status: got %q, want %q", tk.EnrichmentStatus, db.EnrichmentPending)
			}
		}
	}
	if !found {
		t.Error("expected new task created from proposal, not found")
	}
}

func TestAcceptNonPendingFails(t *testing.T) {
	svc, projectID := setupTestService(t)
	ctx := context.Background()

	task, _ := svc.CreateTask(ctx, projectID, "Source", "")
	sug, _ := svc.CreateSuggestion(ctx, projectID, task.ID, db.SuggestionEnrichment, "claude", "X", "msg")

	// Dismiss first
	svc.DismissSuggestion(ctx, projectID, sug.ID)

	// Accepting a dismissed suggestion should fail
	err := svc.AcceptSuggestion(ctx, projectID, sug.ID)
	if err == nil {
		t.Error("expected error accepting non-pending suggestion")
	}
}

func TestMoveTaskService(t *testing.T) {
	svc, projectID := setupTestService(t)
	ctx := context.Background()

	task, _ := svc.CreateTask(ctx, projectID, "Move Through", "")

	statuses := []db.TaskStatus{
		db.StatusBrainstorm,
		db.StatusPlanning,
		db.StatusInProgress,
		db.StatusReview,
		db.StatusDone,
	}

	for _, s := range statuses {
		if err := svc.MoveTask(ctx, projectID, task.ID, s); err != nil {
			t.Fatalf("moving to %s: %v", s, err)
		}
		got, _ := svc.GetTask(ctx, projectID, task.ID)
		if got.Status != s {
			t.Errorf("after move: got %q, want %q", got.Status, s)
		}
	}
}
