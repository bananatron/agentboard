package api

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
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
	r.Use(s.apiKeyMiddleware)

	r.NotFound(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusNotFound, apiError{Error: "not_found"})
	})

	r.Get("/status", s.handleStatus)

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

	return r
}

func (s *Server) apiKeyMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	ctx := r.Context()
	tasks, err := s.svc.ListTasks(ctx)
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
	if suggestions, err := s.svc.ListPendingSuggestions(ctx); err == nil {
		pendingSuggestions = len(suggestions)
	}

	resp := boardSummary{
		Columns:            counts,
		Total:              len(tasks),
		Agents:             agents,
		Enrichments:        enrichments,
		PendingSuggestions: pendingSuggestions,
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleListTasks(w http.ResponseWriter, r *http.Request) {
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
		tasks, err = s.svc.ListTasksByStatus(ctx, stat)
	} else {
		tasks, err = s.svc.ListTasks(ctx)
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

	if deps, err := s.svc.ListAllDependencies(ctx); err == nil {
		for i := range tasks {
			if blockers, ok := deps[tasks[i].ID]; ok {
				tasks[i].BlockedBy = blockers
			}
		}
	}

	writeJSON(w, http.StatusOK, tasks)
}

func (s *Server) handleCreateTask(w http.ResponseWriter, r *http.Request) {
	var req createTaskRequest
	if err := s.decodeJSON(r, &req); err != nil {
		badRequest(w, err)
		return
	}
	if strings.TrimSpace(req.Title) == "" {
		writeJSON(w, http.StatusBadRequest, apiError{Error: "title_required"})
		return
	}

	ctx := r.Context()
	task, err := s.svc.CreateTask(ctx, req.Title, req.Description)
	if err != nil {
		internalError(w, err)
		return
	}

	if req.Enrich {
		pending := db.EnrichmentPending
		if err := s.svc.UpdateTaskFields(ctx, task.ID, db.TaskFieldUpdate{
			EnrichmentStatus: &pending,
		}); err == nil {
			task.EnrichmentStatus = pending
		}
	}

	writeJSON(w, http.StatusCreated, task)
}

func (s *Server) handleGetTask(w http.ResponseWriter, r *http.Request) {
	taskID := chi.URLParam(r, "taskID")
	ctx := r.Context()
	task, err := s.svc.GetTask(ctx, taskID)
	if err != nil {
		handleNotFoundOrError(w, err)
		return
	}

	if deps, err := s.svc.ListDependencies(ctx, taskID); err == nil {
		task.BlockedBy = deps
	}

	writeJSON(w, http.StatusOK, task)
}

func (s *Server) handleUpdateTask(w http.ResponseWriter, r *http.Request) {
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

	if err := s.svc.UpdateTaskFields(r.Context(), taskID, fields); err != nil {
		handleNotFoundOrError(w, err)
		return
	}

	task, err := s.svc.GetTask(r.Context(), taskID)
	if err != nil {
		internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, task)
}

func (s *Server) handleDeleteTask(w http.ResponseWriter, r *http.Request) {
	taskID := chi.URLParam(r, "taskID")
	if err := s.svc.DeleteTask(r.Context(), taskID); err != nil {
		handleNotFoundOrError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"deleted": taskID})
}

func (s *Server) handleClaimTask(w http.ResponseWriter, r *http.Request) {
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

	if err := s.svc.ClaimTask(r.Context(), taskID, req.Assignee); err != nil {
		handleNotFoundOrError(w, err)
		return
	}
	task, err := s.svc.GetTask(r.Context(), taskID)
	if err != nil {
		internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, task)
}

func (s *Server) handleUnclaimTask(w http.ResponseWriter, r *http.Request) {
	taskID := chi.URLParam(r, "taskID")
	if err := s.svc.UnclaimTask(r.Context(), taskID); err != nil {
		handleNotFoundOrError(w, err)
		return
	}
	task, err := s.svc.GetTask(r.Context(), taskID)
	if err != nil {
		internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, task)
}

func (s *Server) handleAgentActivity(w http.ResponseWriter, r *http.Request) {
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
	if err := s.svc.UpdateAgentActivity(r.Context(), taskID, req.Activity); err != nil {
		handleNotFoundOrError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"task_id": taskID, "activity": req.Activity})
}

func (s *Server) handleListComments(w http.ResponseWriter, r *http.Request) {
	taskID := chi.URLParam(r, "taskID")
	comments, err := s.svc.ListComments(r.Context(), taskID)
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
	comment, err := s.svc.AddComment(r.Context(), taskID, req.Author, req.Body)
	if err != nil {
		handleNotFoundOrError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, comment)
}

func (s *Server) handleListDependencies(w http.ResponseWriter, r *http.Request) {
	taskID := chi.URLParam(r, "taskID")
	deps, err := s.svc.ListDependencies(r.Context(), taskID)
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
	if err := s.svc.AddDependency(r.Context(), taskID, req.DependsOn); err != nil {
		handleNotFoundOrError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"task_id": taskID, "blocked_by": req.DependsOn})
}

func (s *Server) handleRemoveDependency(w http.ResponseWriter, r *http.Request) {
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
	if err := s.svc.RemoveDependency(r.Context(), taskID, req.DependsOn); err != nil {
		handleNotFoundOrError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"task_id": taskID, "unblocked": req.DependsOn})
}

func (s *Server) handleCreateSuggestionForTask(w http.ResponseWriter, r *http.Request) {
	taskID := chi.URLParam(r, "taskID")
	var req suggestionRequest
	if err := s.decodeJSON(r, &req); err != nil {
		badRequest(w, err)
		return
	}
	req.TaskID = taskID
	if req.Type == "" {
		req.Type = string(db.SuggestionHint)
	}
	s.handleSuggestionCreate(w, r.Context(), req)
}

func (s *Server) handleListSuggestions(w http.ResponseWriter, r *http.Request) {
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
	suggestions, err := s.svc.ListSuggestions(ctx, status)
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
	var req suggestionRequest
	if err := s.decodeJSON(r, &req); err != nil {
		badRequest(w, err)
		return
	}
	s.handleSuggestionCreate(w, r.Context(), req)
}

func (s *Server) handleSuggestionCreate(w http.ResponseWriter, ctx context.Context, req suggestionRequest) {
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

	sug, err := s.svc.CreateSuggestion(ctx, req.TaskID, sugType, req.Author, req.Title, buildSuggestionMessage(req))
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
	sug, err := s.svc.GetSuggestion(r.Context(), chi.URLParam(r, "suggestionID"))
	if err != nil {
		handleNotFoundOrError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, sug)
}

func (s *Server) handleAcceptSuggestion(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "suggestionID")
	if err := s.svc.AcceptSuggestion(r.Context(), id); err != nil {
		handleNotFoundOrError(w, err)
		return
	}
	sug, _ := s.svc.GetSuggestion(r.Context(), id)
	writeJSON(w, http.StatusOK, map[string]interface{}{"suggestion": sug, "status": "accepted"})
}

func (s *Server) handleDismissSuggestion(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "suggestionID")
	if err := s.svc.DismissSuggestion(r.Context(), id); err != nil {
		handleNotFoundOrError(w, err)
		return
	}
	sug, _ := s.svc.GetSuggestion(r.Context(), id)
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

type createTaskRequest struct {
	Title       string `json:"title"`
	Description string `json:"description"`
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
	TaskID  string `json:"task_id"`
	Type    string `json:"type"`
	Author  string `json:"author"`
	Title   string `json:"title"`
	Message string `json:"message"`
	Reason  string `json:"reason"`
}

type boardSummary struct {
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
