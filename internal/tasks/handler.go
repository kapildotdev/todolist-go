package tasks

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"todolist/internal/auth"
	"todolist/internal/store"
)

type Handler struct {
	store store.Store
}

func New(s store.Store) *Handler { return &Handler{store: s} }

func (h *Handler) Routes(mw func(http.Handler) http.Handler) chi.Router {
	r := chi.NewRouter()
	r.Use(mw)
	r.Get("/", h.list)          // GET /api/tasks?status=&q=&limit=&offset=&since=
	r.Post("/", h.create)       // POST /api/tasks
	r.Patch("/{id}", h.update)  // PATCH /api/tasks/{id}
	r.Delete("/{id}", h.remove) // DELETE
	r.Post("/sync", h.sync)     // POST /api/tasks/sync batch for offline outbox
	return r
}

func uid(r *http.Request) string {
	id, _ := auth.UserID(r)
	return id
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	sinceStr := q.Get("since")
	if sinceStr != "" {
		if ts, err := time.Parse(time.RFC3339, sinceStr); err == nil {
			tasks, err := h.store.ListTasksSince(uid(r), ts)
			if err != nil {
				writeErr(w, 500, "list failed")
				return
			}
			writeJSON(w, 200, tasks)
			return
		}
	}
	limit, _ := strconv.Atoi(q.Get("limit"))
	offset, _ := strconv.Atoi(q.Get("offset"))
	tasks, err := h.store.ListTasks(uid(r), q.Get("status"), q.Get("q"), limit, offset)
	if err != nil {
		writeErr(w, 500, "list failed")
		return
	}
	writeJSON(w, 200, tasks)
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	var in store.TaskInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, 400, "invalid json")
		return
	}
	if in.ID == "" {
		in.ID = uuid.NewString()
	} else if _, err := uuid.Parse(in.ID); err != nil {
		writeErr(w, 400, "invalid id (must be UUID)")
		return
	}
	if in.Title == "" || len(in.Title) > 200 {
		writeErr(w, 400, "title required (1-200 chars)")
		return
	}
	t, err := h.store.CreateTask(store.Task{
		ID: in.ID, UserID: uid(r), Title: in.Title, Description: in.Description,
		Status: orStatus(in.Status), Priority: in.Priority, DueDate: in.DueDate, Position: in.Position,
	})
	if err != nil {
		writeErr(w, 409, "create failed (duplicate id?)")
		return
	}
	writeJSON(w, 201, t)
}

func (h *Handler) update(w http.ResponseWriter, r *http.Request) {
	var in store.TaskInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, 400, "invalid json")
		return
	}
	t, err := h.store.UpdateTask(uid(r), chi.URLParam(r, "id"), in)
	if err == store.ErrNotFound {
		writeErr(w, 404, "not found")
		return
	}
	if err != nil {
		writeErr(w, 500, "update failed")
		return
	}
	writeJSON(w, 200, t)
}

func (h *Handler) remove(w http.ResponseWriter, r *http.Request) {
	if err := h.store.DeleteTask(uid(r), chi.URLParam(r, "id")); err != nil {
		writeErr(w, 404, "not found")
		return
	}
	w.WriteHeader(204)
}

// Sync batch: client sends outbox ops, server applies LWW and returns delta.
type syncReq struct {
	Upserts []store.TaskInput `json:"upserts"`
	Deletes []syncOp          `json:"deletes"`
	// last_pulled_at lets server return only newer changes
	LastPulledAt *time.Time `json:"last_pulled_at"`
}

type syncOp struct {
	ID        string     `json:"id"`
	UpdatedAt *time.Time `json:"updated_at"`
}

type syncResp struct {
	Applied       []store.Task `json:"applied"`
	Conflicts     []store.Task `json:"conflicts"` // server version won
	ServerChanges []store.Task `json:"server_changes"`
	ServerTime    time.Time    `json:"server_time"`
}

func (h *Handler) sync(w http.ResponseWriter, r *http.Request) {
	var req syncReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, 400, "invalid json")
		return
	}
	userID := uid(r)
	var applied, conflicts []store.Task
	for _, u := range req.Upserts {
		t, ok, err := h.store.UpsertSync(userID, u, false)
		if err != nil {
			continue
		}
		if ok {
			applied = append(applied, t)
		} else {
			conflicts = append(conflicts, t)
		}
	}
	for _, d := range req.Deletes {
		t, ok, err := h.store.UpsertSync(userID, store.TaskInput{ID: d.ID, UpdatedAt: d.UpdatedAt}, true)
		if err != nil {
			continue
		}
		if ok {
			applied = append(applied, t)
		} else {
			conflicts = append(conflicts, t)
		}
	}
	if applied == nil {
		applied = []store.Task{}
	}
	if conflicts == nil {
		conflicts = []store.Task{}
	}
	since := time.Time{}
	if req.LastPulledAt != nil {
		since = *req.LastPulledAt
	}
	changes, _ := h.store.ListTasksSince(userID, since)
	if changes == nil {
		changes = []store.Task{}
	}
	writeJSON(w, 200, syncResp{Applied: applied, Conflicts: conflicts, ServerChanges: changes, ServerTime: time.Now().UTC()})
}

func orStatus(s string) string {
	switch s {
	case "doing", "done":
		return s
	default:
		return "todo"
	}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}
