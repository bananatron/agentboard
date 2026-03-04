package api

import (
	"bytes"
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	boardpkg "github.com/markx3/agentboard/internal/board"
	"github.com/markx3/agentboard/internal/db"
)

type Server struct {
	svc       boardpkg.Service
	apiKey    []byte
	handler   http.Handler
	maxBodySz int64
}

func NewServer(svc boardpkg.Service, apiKey string) *Server {
	s := &Server{
		svc:       svc,
		apiKey:    []byte(apiKey),
		maxBodySz: 1 << 20, // 1MB
	}
	s.handler = s.routes()
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.handler.ServeHTTP(w, r)
}

func (s *Server) routes() http.Handler {
	r := chi.NewRouter()

	r.NotFound(s.handleNotFound)

	r.Get("/board", s.handleBoardView)

	r.Group(func(r chi.Router) {
		r.Use(s.apiKeyMiddleware)

		r.Get("/status", s.handleStatus)

		r.Route("/projects", func(r chi.Router) {
			r.Get("/", s.handleListProjects)
			r.Post("/", s.handleCreateProject)

			r.Route("/{projectRef}", func(r chi.Router) {
				r.Get("/", s.handleGetProject)
				r.Put("/", s.handleUpdateProject)
			})
		})

		r.Route("/tasks", func(r chi.Router) {
			r.Get("/", s.handleListTasks)
			r.Post("/", s.handleCreateTask)

			r.Route("/{taskID}", func(r chi.Router) {
				r.Get("/", s.handleGetTask)
				r.Patch("/", s.handleUpdateTask)
				r.Delete("/", s.handleDeleteTask)

				r.Post("/claim", s.handleClaimTask)
				r.Post("/unclaim", s.handleUnclaimTask)
				r.Post("/agent-activity", s.handleAgentActivity)

				r.Get("/comments", s.handleListComments)
				r.Post("/comments", s.handleAddComment)

				r.Get("/dependencies", s.handleListDependencies)
				r.Post("/dependencies", s.handleAddDependency)
				r.Delete("/dependencies", s.handleRemoveDependency)

				r.Post("/suggestions", s.handleCreateSuggestionForTask)
			})
		})

		r.Route("/suggestions", func(r chi.Router) {
			r.Get("/", s.handleListSuggestions)
			r.Post("/", s.handleCreateSuggestion)
			r.Route("/{suggestionID}", func(r chi.Router) {
				r.Get("/", s.handleGetSuggestion)
				r.Post("/accept", s.handleAcceptSuggestion)
				r.Post("/dismiss", s.handleDismissSuggestion)
			})
		})
	})

	return r
}

func (s *Server) apiKeyMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/board") {
			next.ServeHTTP(w, r)
			return
		}
		key := strings.TrimSpace(r.Header.Get("X-API-Key"))
		if key == "" {
			auth := r.Header.Get("Authorization")
			if strings.HasPrefix(strings.ToLower(auth), "bearer ") {
				key = strings.TrimSpace(auth[7:])
			}
		}
		if key == "" || subtleCompare(key, string(s.apiKey)) == false {
			writeJSON(w, http.StatusUnauthorized, apiError{Error: "missing_or_invalid_api_key"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	project, ok := s.requireProject(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	tasks, err := s.svc.ListTasks(ctx, project.ID)
	if err != nil {
		internalError(w, err)
		return
	}

	counts := map[string]int{
		string(db.StatusBacklog):    0,
		string(db.StatusBrainstorm): 0,
		string(db.StatusPlanning):   0,
		string(db.StatusInProgress): 0,
		string(db.StatusReview):     0,
		string(db.StatusDone):       0,
	}

	var agents []agentInfo
	var enrichments []enrichmentInfo
	for _, t := range tasks {
		counts[string(t.Status)]++
		if t.AgentStatus == db.AgentActive {
			agents = append(agents, agentInfo{
				TaskID:    t.ID,
				TaskTitle: t.Title,
				Agent:     t.AgentName,
				Status:    string(t.AgentStatus),
				Column:    string(t.Status),
			})
		}
		if t.EnrichmentStatus != "" && t.EnrichmentStatus != db.EnrichmentNone {
			enrichments = append(enrichments, enrichmentInfo{
				TaskID:    t.ID,
				TaskTitle: t.Title,
				Status:    string(t.EnrichmentStatus),
				Agent:     t.EnrichmentAgentName,
			})
		}
	}

	pendingSuggestions := 0
	if suggestions, err := s.svc.ListPendingSuggestions(ctx, project.ID); err == nil {
		pendingSuggestions = len(suggestions)
	}

	resp := boardSummary{
		Project:            project.Slug,
		Columns:            counts,
		Total:              len(tasks),
		Agents:             agents,
		Enrichments:        enrichments,
		PendingSuggestions: pendingSuggestions,
	}
	s.respond(w, r, http.StatusOK, resp, func(w io.Writer) error {
		return renderStatusText(w, project, tasks, resp)
	})
}

func (s *Server) handleBoardView(w http.ResponseWriter, r *http.Request) {
	slug := strings.TrimSpace(r.URL.Query().Get("project"))
	if slug == "" {
		s.writeBoardHelp(w, r)
		return
	}

	ctx := r.Context()
	project, err := s.svc.GetProjectBySlug(ctx, slug)
	if err != nil {
		s.writeBearNotFound(w, r)
		return
	}

	tasks, err := s.svc.ListTasks(ctx, project.ID)
	if err != nil {
		http.Error(w, "board unavailable", http.StatusInternalServerError)
		return
	}

	var buf bytes.Buffer
	if err := renderTasksBoard(&buf, project, tasks); err != nil {
		http.Error(w, "board unavailable", http.StatusInternalServerError)
		return
	}

	if wantsTextResponse(r) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		w.Write(buf.Bytes())
		return
	}

	body := fmt.Sprintf(`<!DOCTYPE html>
<html>
<head>
  <meta charset="utf-8" />
  <title>%s • agentboard</title>
</head>
<body style="font-family: SFMono-Regular,Consolas,monospace; white-space: pre-wrap;">
<h1>Project: %s</h1>
<pre>%s</pre>
</body>
</html>`,
		html.EscapeString(project.Slug),
		html.EscapeString(project.Slug),
		html.EscapeString(buf.String()))

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	io.WriteString(w, body)
}

func (s *Server) handleNotFound(w http.ResponseWriter, r *http.Request) {
	s.writeBearNotFound(w, r)
}

func (s *Server) handleListProjects(w http.ResponseWriter, r *http.Request) {
	projects, err := s.svc.ListProjects(r.Context())
	if err != nil {
		internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, projects)
}

func (s *Server) handleCreateProject(w http.ResponseWriter, r *http.Request) {
	var req projectCreateRequest
	if err := s.decodeJSON(r, &req); err != nil {
		badRequest(w, err)
		return
	}
	req.Slug = strings.TrimSpace(req.Slug)
	req.Name = strings.TrimSpace(req.Name)
	if req.Slug == "" || req.Name == "" {
		writeJSON(w, http.StatusBadRequest, apiError{Error: "slug_and_name_required"})
		return
	}
	project, err := s.svc.CreateProject(r.Context(), req.Slug, req.Name)
	if err != nil {
		internalError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, project)
}

func (s *Server) handleGetProject(w http.ResponseWriter, r *http.Request) {
	project, err := s.findProjectByRef(r.Context(), chi.URLParam(r, "projectRef"))
	if err != nil {
		handleNotFoundOrError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, project)
}

func (s *Server) handleUpdateProject(w http.ResponseWriter, r *http.Request) {
	projectRef := chi.URLParam(r, "projectRef")
	var req projectUpdateRequest
	if err := s.decodeJSON(r, &req); err != nil {
		badRequest(w, err)
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		writeJSON(w, http.StatusBadRequest, apiError{Error: "name_required"})
		return
	}
	project, err := s.findProjectByRef(r.Context(), projectRef)
	if err != nil {
		handleNotFoundOrError(w, err)
		return
	}
	updated, err := s.svc.RenameProject(r.Context(), project.ID, req.Name)
	if err != nil {
		handleNotFoundOrError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) handleListTasks(w http.ResponseWriter, r *http.Request) {
	project, ok := s.requireProject(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	q := r.URL.Query()

	var (
		tasks []db.Task
		err   error
	)
	status := q.Get("status")
	if status != "" {
		stat := db.TaskStatus(status)
		if !stat.Valid() {
			writeJSON(w, http.StatusBadRequest, apiError{Error: "invalid_status"})
			return
		}
		tasks, err = s.svc.ListTasksByStatus(ctx, project.ID, stat)
	} else {
		tasks, err = s.svc.ListTasks(ctx, project.ID)
	}
	if err != nil {
		internalError(w, err)
		return
	}

	if assignee := q.Get("assignee"); assignee != "" {
		var filtered []db.Task
		for _, t := range tasks {
			if strings.EqualFold(t.Assignee, assignee) {
				filtered = append(filtered, t)
			}
		}
		tasks = filtered
	}

	if search := q.Get("search"); search != "" {
		tasks = filterTasksBySearch(tasks, search)
	}

	if deps, err := s.svc.ListAllDependencies(ctx, project.ID); err == nil {
		for i := range tasks {
			if blockers, ok := deps[tasks[i].ID]; ok {
				tasks[i].BlockedBy = blockers
			}
		}
	}

	s.respond(w, r, http.StatusOK, tasks, func(w io.Writer) error {
		return renderTasksBoard(w, project, tasks)
	})
}

func (s *Server) handleCreateTask(w http.ResponseWriter, r *http.Request) {
	project, ok := s.requireProject(w, r)
	if !ok {
		return
	}
	var req createTaskRequest
	if err := s.decodeJSON(r, &req); err != nil {
		badRequest(w, err)
		return
	}
	if strings.TrimSpace(req.Title) == "" {
		writeJSON(w, http.StatusBadRequest, apiError{Error: "title_required"})
		return
	}

	if strings.TrimSpace(req.ProjectID) == "" {
		writeJSON(w, http.StatusBadRequest, apiError{Error: "project_id_required"})
		return
	}
	if req.ProjectID != project.ID {
		writeJSON(w, http.StatusBadRequest, apiError{Error: "project_mismatch"})
		return
	}

	ctx := r.Context()
	task, err := s.svc.CreateTask(ctx, project.ID, req.Title, req.Description)
	if err != nil {
		internalError(w, err)
		return
	}

	if req.Enrich {
		pending := db.EnrichmentPending
		if err := s.svc.UpdateTaskFields(ctx, project.ID, task.ID, db.TaskFieldUpdate{
			EnrichmentStatus: &pending,
		}); err == nil {
			task.EnrichmentStatus = pending
		}
	}

	writeJSON(w, http.StatusCreated, task)
}

func (s *Server) handleGetTask(w http.ResponseWriter, r *http.Request) {
	project, ok := s.requireProject(w, r)
	if !ok {
		return
	}
	taskID := chi.URLParam(r, "taskID")
	ctx := r.Context()
	task, err := s.svc.GetTask(ctx, project.ID, taskID)
	if err != nil {
		handleNotFoundOrError(w, err)
		return
	}

	if deps, err := s.svc.ListDependencies(ctx, project.ID, taskID); err == nil {
		task.BlockedBy = deps
	}

	writeJSON(w, http.StatusOK, task)
}

func (s *Server) handleUpdateTask(w http.ResponseWriter, r *http.Request) {
	project, ok := s.requireProject(w, r)
	if !ok {
		return
	}
	taskID := chi.URLParam(r, "taskID")
	var req updateTaskRequest
	if err := s.decodeJSON(r, &req); err != nil {
		badRequest(w, err)
		return
	}

	fields, err := req.toTaskFieldUpdate()
	if err != nil {
		writeJSON(w, http.StatusBadRequest, apiError{Error: err.Error()})
		return
	}

	if err := s.svc.UpdateTaskFields(r.Context(), project.ID, taskID, fields); err != nil {
		handleNotFoundOrError(w, err)
		return
	}

	task, err := s.svc.GetTask(r.Context(), project.ID, taskID)
	if err != nil {
		internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, task)
}

func (s *Server) handleDeleteTask(w http.ResponseWriter, r *http.Request) {
	project, ok := s.requireProject(w, r)
	if !ok {
		return
	}
	taskID := chi.URLParam(r, "taskID")
	if err := s.svc.DeleteTask(r.Context(), project.ID, taskID); err != nil {
		handleNotFoundOrError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"deleted": taskID})
}

func (s *Server) handleClaimTask(w http.ResponseWriter, r *http.Request) {
	project, ok := s.requireProject(w, r)
	if !ok {
		return
	}
	taskID := chi.URLParam(r, "taskID")
	var req struct {
		Assignee string `json:"assignee"`
	}
	if err := s.decodeJSON(r, &req); err != nil {
		badRequest(w, err)
		return
	}
	if strings.TrimSpace(req.Assignee) == "" {
		writeJSON(w, http.StatusBadRequest, apiError{Error: "assignee_required"})
		return
	}

	if err := s.svc.ClaimTask(r.Context(), project.ID, taskID, req.Assignee); err != nil {
		handleNotFoundOrError(w, err)
		return
	}
	task, err := s.svc.GetTask(r.Context(), project.ID, taskID)
	if err != nil {
		internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, task)
}

func (s *Server) handleUnclaimTask(w http.ResponseWriter, r *http.Request) {
	project, ok := s.requireProject(w, r)
	if !ok {
		return
	}
	taskID := chi.URLParam(r, "taskID")
	if err := s.svc.UnclaimTask(r.Context(), project.ID, taskID); err != nil {
		handleNotFoundOrError(w, err)
		return
	}
	task, err := s.svc.GetTask(r.Context(), project.ID, taskID)
	if err != nil {
		internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, task)
}

func (s *Server) handleAgentActivity(w http.ResponseWriter, r *http.Request) {
	project, ok := s.requireProject(w, r)
	if !ok {
		return
	}
	taskID := chi.URLParam(r, "taskID")
	var req struct {
		Activity string `json:"activity"`
	}
	if err := s.decodeJSON(r, &req); err != nil {
		badRequest(w, err)
		return
	}
	if len(req.Activity) > 200 {
		req.Activity = req.Activity[:200]
	}
	if err := s.svc.UpdateAgentActivity(r.Context(), project.ID, taskID, req.Activity); err != nil {
		handleNotFoundOrError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"task_id": taskID, "activity": req.Activity})
}

func (s *Server) handleListComments(w http.ResponseWriter, r *http.Request) {
	project, ok := s.requireProject(w, r)
	if !ok {
		return
	}
	taskID := chi.URLParam(r, "taskID")
	comments, err := s.svc.ListComments(r.Context(), project.ID, taskID)
	if err != nil {
		handleNotFoundOrError(w, err)
		return
	}
	if comments == nil {
		comments = []db.Comment{}
	}
	writeJSON(w, http.StatusOK, comments)
}

func (s *Server) handleAddComment(w http.ResponseWriter, r *http.Request) {
	project, ok := s.requireProject(w, r)
	if !ok {
		return
	}
	taskID := chi.URLParam(r, "taskID")
	var req struct {
		Author string `json:"author"`
		Body   string `json:"body"`
	}
	if err := s.decodeJSON(r, &req); err != nil {
		badRequest(w, err)
		return
	}
	if strings.TrimSpace(req.Author) == "" || strings.TrimSpace(req.Body) == "" {
		writeJSON(w, http.StatusBadRequest, apiError{Error: "author_and_body_required"})
		return
	}
	comment, err := s.svc.AddComment(r.Context(), project.ID, taskID, req.Author, req.Body)
	if err != nil {
		handleNotFoundOrError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, comment)
}

func (s *Server) handleListDependencies(w http.ResponseWriter, r *http.Request) {
	project, ok := s.requireProject(w, r)
	if !ok {
		return
	}
	taskID := chi.URLParam(r, "taskID")
	deps, err := s.svc.ListDependencies(r.Context(), project.ID, taskID)
	if err != nil {
		handleNotFoundOrError(w, err)
		return
	}
	if deps == nil {
		deps = []string{}
	}
	writeJSON(w, http.StatusOK, map[string][]string{"blocked_by": deps})
}

func (s *Server) handleAddDependency(w http.ResponseWriter, r *http.Request) {
	project, ok := s.requireProject(w, r)
	if !ok {
		return
	}
	taskID := chi.URLParam(r, "taskID")
	var req struct {
		DependsOn string `json:"depends_on"`
	}
	if err := s.decodeJSON(r, &req); err != nil {
		badRequest(w, err)
		return
	}
	if strings.TrimSpace(req.DependsOn) == "" {
		writeJSON(w, http.StatusBadRequest, apiError{Error: "depends_on_required"})
		return
	}
	if err := s.svc.AddDependency(r.Context(), project.ID, taskID, req.DependsOn); err != nil {
		handleNotFoundOrError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"task_id": taskID, "blocked_by": req.DependsOn})
}

func (s *Server) handleRemoveDependency(w http.ResponseWriter, r *http.Request) {
	project, ok := s.requireProject(w, r)
	if !ok {
		return
	}
	taskID := chi.URLParam(r, "taskID")
	var req struct {
		DependsOn string `json:"depends_on"`
	}
	if err := s.decodeJSON(r, &req); err != nil {
		badRequest(w, err)
		return
	}
	if strings.TrimSpace(req.DependsOn) == "" {
		writeJSON(w, http.StatusBadRequest, apiError{Error: "depends_on_required"})
		return
	}
	if err := s.svc.RemoveDependency(r.Context(), project.ID, taskID, req.DependsOn); err != nil {
		handleNotFoundOrError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"task_id": taskID, "unblocked": req.DependsOn})
}

func (s *Server) handleCreateSuggestionForTask(w http.ResponseWriter, r *http.Request) {
	project, ok := s.requireProject(w, r)
	if !ok {
		return
	}
	taskID := chi.URLParam(r, "taskID")
	var req suggestionRequest
	if err := s.decodeJSON(r, &req); err != nil {
		badRequest(w, err)
		return
	}
	if strings.TrimSpace(req.ProjectID) == "" {
		writeJSON(w, http.StatusBadRequest, apiError{Error: "project_id_required"})
		return
	}
	if req.ProjectID != project.ID {
		writeJSON(w, http.StatusBadRequest, apiError{Error: "project_mismatch"})
		return
	}
	req.TaskID = taskID
	if req.Type == "" {
		req.Type = string(db.SuggestionHint)
	}
	s.handleSuggestionCreate(w, r, project, req)
}

func (s *Server) handleListSuggestions(w http.ResponseWriter, r *http.Request) {
	project, ok := s.requireProject(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	q := r.URL.Query()
	status := db.SuggestionStatus(q.Get("status"))
	if status == "" {
		status = db.SuggestionPending
	}
	if !status.Valid() {
		writeJSON(w, http.StatusBadRequest, apiError{Error: "invalid_status"})
		return
	}
	suggestions, err := s.svc.ListSuggestions(ctx, project.ID, status)
	if err != nil {
		internalError(w, err)
		return
	}

	taskID := q.Get("task_id")
	if taskID != "" {
		var filtered []db.Suggestion
		for _, s := range suggestions {
			if s.TaskID == taskID {
				filtered = append(filtered, s)
			}
		}
		suggestions = filtered
	}

	if suggestions == nil {
		suggestions = []db.Suggestion{}
	}
	writeJSON(w, http.StatusOK, suggestions)
}

func (s *Server) handleCreateSuggestion(w http.ResponseWriter, r *http.Request) {
	project, ok := s.requireProject(w, r)
	if !ok {
		return
	}
	var req suggestionRequest
	if err := s.decodeJSON(r, &req); err != nil {
		badRequest(w, err)
		return
	}
	if strings.TrimSpace(req.ProjectID) == "" {
		writeJSON(w, http.StatusBadRequest, apiError{Error: "project_id_required"})
		return
	}
	if req.ProjectID != project.ID {
		writeJSON(w, http.StatusBadRequest, apiError{Error: "project_mismatch"})
		return
	}
	s.handleSuggestionCreate(w, r, project, req)
}

func (s *Server) handleSuggestionCreate(w http.ResponseWriter, r *http.Request, project *db.Project, req suggestionRequest) {
	ctx := r.Context()
	if req.Type == "" {
		writeJSON(w, http.StatusBadRequest, apiError{Error: "type_required"})
		return
	}
	sugType := db.SuggestionType(req.Type)
	if !sugType.Valid() {
		writeJSON(w, http.StatusBadRequest, apiError{Error: "invalid_type"})
		return
	}
	if strings.TrimSpace(req.Title) == "" {
		writeJSON(w, http.StatusBadRequest, apiError{Error: "title_required"})
		return
	}
	if strings.TrimSpace(req.Message) == "" {
		writeJSON(w, http.StatusBadRequest, apiError{Error: "message_required"})
		return
	}

	if req.TaskID == "" && sugType != db.SuggestionProposal {
		writeJSON(w, http.StatusBadRequest, apiError{Error: "task_id_required"})
		return
	}

	sug, err := s.svc.CreateSuggestion(ctx, project.ID, req.TaskID, sugType, req.Author, req.Title, buildSuggestionMessage(req))
	if err != nil {
		handleNotFoundOrError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, sug)
}

func buildSuggestionMessage(req suggestionRequest) string {
	if strings.TrimSpace(req.Reason) == "" {
		return req.Message
	}
	return strings.TrimSpace(req.Message) + "\n\nReason: " + strings.TrimSpace(req.Reason)
}

func (s *Server) handleGetSuggestion(w http.ResponseWriter, r *http.Request) {
	project, ok := s.requireProject(w, r)
	if !ok {
		return
	}
	sug, err := s.svc.GetSuggestion(r.Context(), project.ID, chi.URLParam(r, "suggestionID"))
	if err != nil {
		handleNotFoundOrError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, sug)
}

func (s *Server) handleAcceptSuggestion(w http.ResponseWriter, r *http.Request) {
	project, ok := s.requireProject(w, r)
	if !ok {
		return
	}
	id := chi.URLParam(r, "suggestionID")
	if err := s.svc.AcceptSuggestion(r.Context(), project.ID, id); err != nil {
		handleNotFoundOrError(w, err)
		return
	}
	sug, _ := s.svc.GetSuggestion(r.Context(), project.ID, id)
	writeJSON(w, http.StatusOK, map[string]interface{}{"suggestion": sug, "status": "accepted"})
}

func (s *Server) handleDismissSuggestion(w http.ResponseWriter, r *http.Request) {
	project, ok := s.requireProject(w, r)
	if !ok {
		return
	}
	id := chi.URLParam(r, "suggestionID")
	if err := s.svc.DismissSuggestion(r.Context(), project.ID, id); err != nil {
		handleNotFoundOrError(w, err)
		return
	}
	sug, _ := s.svc.GetSuggestion(r.Context(), project.ID, id)
	writeJSON(w, http.StatusOK, map[string]interface{}{"suggestion": sug, "status": "dismissed"})
}

func (s *Server) decodeJSON(r *http.Request, v interface{}) error {
	defer r.Body.Close()
	limited := io.LimitReader(r.Body, s.maxBodySz)
	dec := json.NewDecoder(limited)
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

func handleNotFoundOrError(w http.ResponseWriter, err error) {
	if isNotFound(err) {
		writeJSON(w, http.StatusNotFound, apiError{Error: "not_found"})
		return
	}
	internalError(w, err)
}

func isNotFound(err error) bool {
	return errors.Is(err, sql.ErrNoRows)
}

func internalError(w http.ResponseWriter, err error) {
	writeJSON(w, http.StatusInternalServerError, apiError{Error: "internal_error"})
}

func badRequest(w http.ResponseWriter, err error) {
	writeJSON(w, http.StatusBadRequest, apiError{Error: err.Error()})
}

func writeJSON(w http.ResponseWriter, status int, payload interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if payload == nil {
		return
	}
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		http.Error(w, `{"error":"encoding_error"}`, http.StatusInternalServerError)
	}
}

type apiError struct {
	Error string `json:"error"`
}

func (s *Server) respond(w http.ResponseWriter, r *http.Request, status int, payload interface{}, text func(io.Writer) error) {
	if wantsTextResponse(r) && text != nil {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(status)
		if err := text(w); err != nil {
			http.Error(w, "render_error", http.StatusInternalServerError)
		}
		return
	}
	writeJSON(w, status, payload)
}

func wantsTextResponse(r *http.Request) bool {
	if strings.EqualFold(r.URL.Query().Get("format"), "text") {
		return true
	}
	accept := r.Header.Get("Accept")
	return strings.Contains(strings.ToLower(accept), "text/plain")
}

func prefersHTML(r *http.Request) bool {
	accept := strings.ToLower(r.Header.Get("Accept"))
	return strings.Contains(accept, "text/html")
}

func (s *Server) writeBoardHelp(w http.ResponseWriter, r *http.Request) {
	message := "To view a board, supply ?project=<slug>."
	if prefersHTML(r) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(w, "<!DOCTYPE html><html><body style=\"font-family:monospace;\">%s</body></html>", html.EscapeString(message))
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusBadRequest)
	fmt.Fprintln(w, message)
}

func (s *Server) writeBearNotFound(w http.ResponseWriter, r *http.Request) {
	bear := "ʕノ•ᴥ•ʔノ◕"
	message := fmt.Sprintf("%s 404 - nothing to see here.", bear)
	if prefersHTML(r) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprintf(w, "<!DOCTYPE html><html><body style=\"font-family:monospace; white-space: pre-wrap;\"><pre>%s</pre></body></html>", html.EscapeString(message))
		return
	}
	if wantsTextResponse(r) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprintln(w, message)
		return
	}
	writeJSON(w, http.StatusNotFound, apiError{Error: "not_found"})
}

func (s *Server) requireProject(w http.ResponseWriter, r *http.Request) (*db.Project, bool) {
	ctx := r.Context()
	if slug := strings.TrimSpace(r.Header.Get("X-Agentboard-Project")); slug != "" {
		project, err := s.svc.GetProjectBySlug(ctx, slug)
		if err != nil {
			handleNotFoundOrError(w, err)
			return nil, false
		}
		return project, true
	}
	if id := strings.TrimSpace(r.URL.Query().Get("project_id")); id != "" {
		project, err := s.svc.GetProjectByID(ctx, id)
		if err != nil {
			handleNotFoundOrError(w, err)
			return nil, false
		}
		return project, true
	}
	writeJSON(w, http.StatusBadRequest, apiError{Error: "project_required"})
	return nil, false
}

func (s *Server) findProjectByRef(ctx context.Context, ref string) (*db.Project, error) {
	if ref == "" {
		return nil, sql.ErrNoRows
	}
	if project, err := s.svc.GetProjectByID(ctx, ref); err == nil {
		return project, nil
	}
	return s.svc.GetProjectBySlug(ctx, ref)
}

func renderTasksBoard(w io.Writer, project *db.Project, tasks []db.Task) error {
	statuses := []db.TaskStatus{
		db.StatusBacklog,
		db.StatusBrainstorm,
		db.StatusPlanning,
		db.StatusInProgress,
		db.StatusReview,
		db.StatusDone,
	}
	fmt.Fprintf(w, "Project: %s\n\n", project.Slug)
	for _, status := range statuses {
		fmt.Fprintf(w, "%s\n", strings.ToUpper(string(status)))
		for _, t := range tasks {
			if t.Status == status {
				fmt.Fprintf(w, "- [%s] %s\n", t.ID[:min(8, len(t.ID))], strings.TrimSpace(t.Title))
			}
		}
		fmt.Fprintln(w)
	}
	return nil
}

func renderStatusText(w io.Writer, project *db.Project, tasks []db.Task, summary boardSummary) error {
	fmt.Fprintf(w, "Project: %s (%d tasks)\n\n", project.Slug, summary.Total)
	if len(summary.Agents) > 0 {
		fmt.Fprintln(w, "Active Agents:")
		for _, agent := range summary.Agents {
			fmt.Fprintf(w, "- %s: %s (%s)\n", agent.Agent, agent.TaskTitle, agent.Column)
		}
		fmt.Fprintln(w)
	}
	return renderTasksBoard(w, project, tasks)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

type createTaskRequest struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	ProjectID   string `json:"project_id"`
	Enrich      bool   `json:"enrich"`
}

type updateTaskRequest struct {
	Title               *string `json:"title"`
	Description         *string `json:"description"`
	Status              *string `json:"status"`
	Assignee            *string `json:"assignee"`
	BranchName          *string `json:"branch"`
	PRUrl               *string `json:"pr_url"`
	PRNumber            *int    `json:"pr_number"`
	EnrichmentStatus    *string `json:"enrichment_status"`
	EnrichmentAgentName *string `json:"enrichment_agent"`
}

func (req *updateTaskRequest) toTaskFieldUpdate() (db.TaskFieldUpdate, error) {
	var fields db.TaskFieldUpdate
	if req.Title != nil {
		fields.Title = req.Title
	}
	if req.Description != nil {
		fields.Description = req.Description
	}
	if req.Status != nil {
		status := db.TaskStatus(*req.Status)
		if !status.Valid() {
			return fields, fmt.Errorf("invalid_status")
		}
		fields.Status = &status
	}
	if req.Assignee != nil {
		fields.Assignee = req.Assignee
	}
	if req.BranchName != nil {
		fields.BranchName = req.BranchName
	}
	if req.PRUrl != nil {
		fields.PRUrl = req.PRUrl
	}
	if req.PRNumber != nil {
		fields.PRNumber = req.PRNumber
	}
	if req.EnrichmentStatus != nil {
		status := db.EnrichmentStatus(*req.EnrichmentStatus)
		if !status.Valid() {
			return fields, fmt.Errorf("invalid_enrichment_status")
		}
		fields.EnrichmentStatus = &status
	}
	if req.EnrichmentAgentName != nil {
		fields.EnrichmentAgentName = req.EnrichmentAgentName
	}
	return fields, nil
}

type suggestionRequest struct {
	ProjectID string `json:"project_id"`
	TaskID    string `json:"task_id"`
	Type      string `json:"type"`
	Author    string `json:"author"`
	Title     string `json:"title"`
	Message   string `json:"message"`
	Reason    string `json:"reason"`
}

type projectCreateRequest struct {
	Slug string `json:"slug"`
	Name string `json:"name"`
}

type projectUpdateRequest struct {
	Name string `json:"name"`
}

type boardSummary struct {
	Project            string           `json:"project"`
	Columns            map[string]int   `json:"columns"`
	Total              int              `json:"total"`
	Agents             []agentInfo      `json:"agents,omitempty"`
	Enrichments        []enrichmentInfo `json:"enrichments,omitempty"`
	PendingSuggestions int              `json:"pending_suggestions"`
}

type agentInfo struct {
	TaskID    string `json:"task_id"`
	TaskTitle string `json:"task_title"`
	Agent     string `json:"agent"`
	Status    string `json:"status"`
	Column    string `json:"column"`
}

type enrichmentInfo struct {
	TaskID    string `json:"task_id"`
	TaskTitle string `json:"task_title"`
	Status    string `json:"status"`
	Agent     string `json:"agent,omitempty"`
}

func filterTasksBySearch(tasks []db.Task, query string) []db.Task {
	lq := strings.ToLower(query)
	out := make([]db.Task, 0, len(tasks))
	for _, t := range tasks {
		if strings.Contains(strings.ToLower(t.Title), lq) ||
			strings.Contains(strings.ToLower(t.Description), lq) {
			out = append(out, t)
		}
	}
	return out
}

func subtleCompare(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
