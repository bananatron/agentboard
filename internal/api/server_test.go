package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	boardpkg "github.com/markx3/agentboard/internal/board"
	"github.com/markx3/agentboard/internal/db"
)

const testAPIKey = "super-secret"

func newTestServer(t *testing.T) (*Server, func()) {
	t.Helper()
	dir := t.TempDir()
	database, err := db.Open(filepath.Join(dir, "board.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	svc := boardpkg.NewLocalService(database)
	srv := NewServer(svc, testAPIKey)
	return srv, func() {
		database.Close()
	}
}

func TestAPIRequiresKey(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodGet, "/status", nil)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
}

func TestAPITaskLifecycle(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()

	// Create task via API.
	createBody := map[string]any{
		"title":       "Test Task",
		"description": "from api",
	}
	resp := doJSONRequest(t, srv, http.MethodPost, "/tasks", createBody)
	if resp.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", resp.Code, resp.Body.String())
	}
	var created db.Task
	decodeBody(t, resp, &created)

	// Get task by ID.
	getResp := doJSONRequest(t, srv, http.MethodGet, "/tasks/"+created.ID, nil)
	if getResp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", getResp.Code, getResp.Body.String())
	}
	var fetched db.Task
	decodeBody(t, getResp, &fetched)
	if fetched.ID != created.ID {
		t.Fatalf("expected task %s, got %s", created.ID, fetched.ID)
	}

	// Update status to in_progress.
	updateBody := map[string]any{"status": "in_progress"}
	updateResp := doJSONRequest(t, srv, http.MethodPatch, "/tasks/"+created.ID, updateBody)
	if updateResp.Code != http.StatusOK {
		t.Fatalf("expected 200 on update, got %d: %s", updateResp.Code, updateResp.Body.String())
	}
	decodeBody(t, updateResp, &fetched)
	if fetched.Status != db.StatusInProgress {
		t.Fatalf("expected status in_progress, got %s", fetched.Status)
	}

	// Add a comment.
	commentBody := map[string]any{"author": "tester", "body": "looks good"}
	commentResp := doJSONRequest(t, srv, http.MethodPost, "/tasks/"+created.ID+"/comments", commentBody)
	if commentResp.Code != http.StatusCreated {
		t.Fatalf("expected 201 comment, got %d: %s", commentResp.Code, commentResp.Body.String())
	}

	commentsResp := doJSONRequest(t, srv, http.MethodGet, "/tasks/"+created.ID+"/comments", nil)
	if commentsResp.Code != http.StatusOK {
		t.Fatalf("expected 200 comments, got %d", commentsResp.Code)
	}
	var comments []db.Comment
	decodeBody(t, commentsResp, &comments)
	if len(comments) != 1 || comments[0].Author != "tester" {
		t.Fatalf("expected 1 comment from tester, got %#v", comments)
	}

	// Verify list endpoint returns filtered search results.
	listResp := doJSONRequest(t, srv, http.MethodGet, "/tasks?search=api", nil)
	if listResp.Code != http.StatusOK {
		t.Fatalf("expected 200 list, got %d", listResp.Code)
	}
	var tasks []db.Task
	decodeBody(t, listResp, &tasks)
	if len(tasks) != 1 || tasks[0].ID != created.ID {
		t.Fatalf("expected created task in search results, got %#v", tasks)
	}
}

func TestAPISuggestionFlow(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()

	// Seed a task.
	taskResp := doJSONRequest(t, srv, http.MethodPost, "/tasks", map[string]any{
		"title": "Need suggestion",
	})
	if taskResp.Code != http.StatusCreated {
		t.Fatalf("expected 201 task, got %d", taskResp.Code)
	}
	var task db.Task
	decodeBody(t, taskResp, &task)

	// Create a suggestion for that task.
	sugResp := doJSONRequest(t, srv, http.MethodPost, "/tasks/"+task.ID+"/suggestions", map[string]any{
		"title":   "Try something",
		"message": "maybe refactor",
		"author":  "bot",
	})
	if sugResp.Code != http.StatusCreated {
		t.Fatalf("expected 201 suggestion, got %d: %s", sugResp.Code, sugResp.Body.String())
	}
	var suggestion db.Suggestion
	decodeBody(t, sugResp, &suggestion)

	// List pending suggestions.
	listResp := doJSONRequest(t, srv, http.MethodGet, "/suggestions?status=pending", nil)
	if listResp.Code != http.StatusOK {
		t.Fatalf("expected 200 suggestions, got %d", listResp.Code)
	}
	var suggestions []db.Suggestion
	decodeBody(t, listResp, &suggestions)
	if len(suggestions) != 1 || suggestions[0].ID != suggestion.ID {
		t.Fatalf("expected suggestion in list, got %#v", suggestions)
	}

	// Accept the suggestion.
	accResp := doJSONRequest(t, srv, http.MethodPost, "/suggestions/"+suggestion.ID+"/accept", nil)
	if accResp.Code != http.StatusOK {
		t.Fatalf("expected 200 accept, got %d: %s", accResp.Code, accResp.Body.String())
	}

	getResp := doJSONRequest(t, srv, http.MethodGet, "/suggestions/"+suggestion.ID, nil)
	if getResp.Code != http.StatusOK {
		t.Fatalf("expected 200 suggestion get, got %d: %s", getResp.Code, getResp.Body.String())
	}
	var accepted db.Suggestion
	decodeBody(t, getResp, &accepted)
	if accepted.Status != db.SuggestionAccepted {
		t.Fatalf("expected accepted status, got %s", accepted.Status)
	}
}

func TestAPIDependencies(t *testing.T) {
	srv, cleanup := newTestServer(t)
	defer cleanup()

	taskA := createTask(t, srv, "Task A")
	taskB := createTask(t, srv, "Task B")

	body := map[string]any{"depends_on": taskB.ID}
	addResp := doJSONRequest(t, srv, http.MethodPost, "/tasks/"+taskA.ID+"/dependencies", body)
	if addResp.Code != http.StatusOK {
		t.Fatalf("expected 200 add dependency, got %d: %s", addResp.Code, addResp.Body.String())
	}

	listResp := doJSONRequest(t, srv, http.MethodGet, "/tasks/"+taskA.ID+"/dependencies", nil)
	if listResp.Code != http.StatusOK {
		t.Fatalf("expected 200 get dependencies, got %d", listResp.Code)
	}
	var result struct {
		BlockedBy []string `json:"blocked_by"`
	}
	decodeBody(t, listResp, &result)
	if len(result.BlockedBy) != 1 || result.BlockedBy[0] != taskB.ID {
		t.Fatalf("expected dependency on %s, got %#v", taskB.ID, result.BlockedBy)
	}

	delResp := doJSONRequest(t, srv, http.MethodDelete, "/tasks/"+taskA.ID+"/dependencies", body)
	if delResp.Code != http.StatusOK {
		t.Fatalf("expected 200 remove dependency, got %d: %s", delResp.Code, delResp.Body.String())
	}
}

func createTask(t *testing.T, srv *Server, title string) db.Task {
	t.Helper()
	resp := doJSONRequest(t, srv, http.MethodPost, "/tasks", map[string]any{
		"title": title,
	})
	if resp.Code != http.StatusCreated {
		t.Fatalf("expected task created, got %d: %s", resp.Code, resp.Body.String())
	}
	var task db.Task
	decodeBody(t, resp, &task)
	return task
}

func doJSONRequest(t *testing.T, srv *Server, method, path string, body interface{}) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		reader = bytes.NewReader(data)
	} else {
		reader = bytes.NewReader(nil)
	}

	req := httptest.NewRequest(method, path, reader)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("X-API-Key", testAPIKey)

	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	return rr
}

func decodeBody(t *testing.T, resp *httptest.ResponseRecorder, out interface{}) {
	t.Helper()
	if err := json.Unmarshal(resp.Body.Bytes(), out); err != nil {
		t.Fatalf("decode body: %v\npayload: %s", err, resp.Body.String())
	}
}
