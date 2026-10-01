package store

import (
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// MemoryStore lets the app run with no DATABASE_URL (local dev / offline demo).
type MemoryStore struct {
	mu       sync.RWMutex
	users    map[string]User            // id -> user
	byEmail  map[string]string          // email -> id
	refresh  map[string]refreshEntry    // tokenHash -> entry
	tasks    map[string]map[string]Task // userID -> taskID -> Task
}

type refreshEntry struct {
	userID    string
	expiresAt time.Time
	revoked   bool
}

func NewMemory() *MemoryStore {
	return &MemoryStore{
		users:   map[string]User{},
		byEmail: map[string]string{},
		refresh: map[string]refreshEntry{},
		tasks:   map[string]map[string]Task{},
	}
}

func (m *MemoryStore) CreateUser(email, hash string) (User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	email = strings.ToLower(strings.TrimSpace(email))
	if _, ok := m.byEmail[email]; ok {
		return User{}, ErrConflict
	}
	u := User{ID: uuid.NewString(), Email: email, PasswordHash: hash, CreatedAt: time.Now().UTC()}
	m.users[u.ID] = u
	m.byEmail[email] = u.ID
	return u, nil
}

func (m *MemoryStore) FindUserByEmail(email string) (User, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	id, ok := m.byEmail[strings.ToLower(strings.TrimSpace(email))]
	if !ok {
		return User{}, ErrNotFound
	}
	return m.users[id], nil
}

func (m *MemoryStore) FindUserByID(id string) (User, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	u, ok := m.users[id]
	if !ok {
		return User{}, ErrNotFound
	}
	return u, nil
}

func (m *MemoryStore) SaveRefreshToken(userID, tokenHash string, expiresAt time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.refresh[tokenHash] = refreshEntry{userID: userID, expiresAt: expiresAt}
	return nil
}

func (m *MemoryStore) FindUserIDByRefreshHash(tokenHash string) (string, time.Time, bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	e, ok := m.refresh[tokenHash]
	if !ok {
		return "", time.Time{}, false, ErrNotFound
	}
	return e.userID, e.expiresAt, e.revoked, nil
}

func (m *MemoryStore) RevokeRefreshToken(tokenHash string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if e, ok := m.refresh[tokenHash]; ok {
		e.revoked = true
		m.refresh[tokenHash] = e
	}
	return nil
}

func (m *MemoryStore) RevokeAllUserTokens(userID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for k, e := range m.refresh {
		if e.userID == userID {
			e.revoked = true
			m.refresh[k] = e
		}
	}
	return nil
}

func normStatus(s string) string {
	switch s {
	case "doing", "done":
		return s
	default:
		return "todo"
	}
}

func (m *MemoryStore) CreateTask(t Task) (Task, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if t.ID == "" {
		t.ID = uuid.NewString()
	}
	if t.Title == "" || len(t.Title) > 200 {
		return Task{}, ErrConflict
	}
	if t.Status == "" {
		t.Status = "todo"
	}
	t.Status = normStatus(t.Status)
	t.Version = 1
	t.UpdatedAt = time.Now().UTC()
	if m.tasks[t.UserID] == nil {
		m.tasks[t.UserID] = map[string]Task{}
	}
	if _, exists := m.tasks[t.UserID][t.ID]; exists {
		return Task{}, ErrConflict
	}
	m.tasks[t.UserID][t.ID] = t
	return t, nil
}

func (m *MemoryStore) ListTasks(userID, status, query string, limit, offset int) ([]Task, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []Task
	for _, t := range m.tasks[userID] {
		if t.DeletedAt != nil {
			continue
		}
		if status != "" && status != "all" && t.Status != status {
			continue
		}
		if query != "" && !strings.Contains(strings.ToLower(t.Title+" "+t.Description), strings.ToLower(query)) {
			continue
		}
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Position == out[j].Position {
			return out[i].UpdatedAt.After(out[j].UpdatedAt)
		}
		return out[i].Position < out[j].Position
	})
	if limit <= 0 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	if offset > len(out) {
		return []Task{}, nil
	}
	end := offset + limit
	if end > len(out) {
		end = len(out)
	}
	return out[offset:end], nil
}

func (m *MemoryStore) ListTasksSince(userID string, since time.Time) ([]Task, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []Task
	for _, t := range m.tasks[userID] {
		if t.UpdatedAt.After(since) {
			out = append(out, t)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt.Before(out[j].UpdatedAt) })
	return out, nil
}

func (m *MemoryStore) GetTask(userID, id string) (Task, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	t, ok := m.tasks[userID][id]
	if !ok || t.DeletedAt != nil {
		return Task{}, ErrNotFound
	}
	return t, nil
}

func (m *MemoryStore) UpdateTask(userID, id string, in TaskInput) (Task, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.tasks[userID][id]
	if !ok || t.DeletedAt != nil {
		return Task{}, ErrNotFound
	}
	if in.Title != "" {
		t.Title = in.Title
	}
	t.Description = in.Description
	if in.Status != "" {
		t.Status = normStatus(in.Status)
	}
	t.Priority = in.Priority
	t.DueDate = in.DueDate
	t.Position = in.Position
	t.Version++
	t.UpdatedAt = time.Now().UTC()
	m.tasks[userID][id] = t
	return t, nil
}

func (m *MemoryStore) DeleteTask(userID, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.tasks[userID][id]
	if !ok {
		return ErrNotFound
	}
	now := time.Now().UTC()
	t.DeletedAt = &now
	t.UpdatedAt = now
	t.Version++
	m.tasks[userID][id] = t
	return nil
}

// UpsertSync implements Last-Write-Wins by UpdatedAt.
func (m *MemoryStore) UpsertSync(userID string, in TaskInput, deleted bool) (Task, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.tasks[userID] == nil {
		m.tasks[userID] = map[string]Task{}
	}
	id := in.ID
	if id == "" {
		id = uuid.NewString()
	}
	clientTS := time.Now().UTC()
	if in.UpdatedAt != nil {
		clientTS = *in.UpdatedAt
	}
	existing, ok := m.tasks[userID][id]
	if !ok {
		now := time.Now().UTC()
		t := Task{ID: id, UserID: userID, Title: in.Title, Description: in.Description,
			Status: normStatus(orDefault(in.Status, "todo")), Priority: in.Priority,
			DueDate: in.DueDate, Position: in.Position, Version: 1, UpdatedAt: now}
		if t.Title == "" && !deleted {
			t.Title = "(untitled)"
		}
		if deleted {
			t.DeletedAt = &now
			t.UpdatedAt = now
		}
		m.tasks[userID][id] = t
		return t, true, nil
	}
	// LWW: client wins only if strictly newer
	if !clientTS.After(existing.UpdatedAt) {
		return existing, false, nil // conflict: server kept
	}
	existing.Description = in.Description
	if in.Title != "" {
		existing.Title = in.Title
	}
	if in.Status != "" {
		existing.Status = normStatus(in.Status)
	}
	existing.Priority = in.Priority
	existing.DueDate = in.DueDate
	existing.Position = in.Position
	existing.Version++
	now := time.Now().UTC()
	existing.UpdatedAt = now
	if deleted {
		existing.DeletedAt = &now
	} else {
		existing.DeletedAt = nil
	}
	m.tasks[userID][id] = existing
	return existing, true, nil
}

func orDefault(s, d string) string {
	if s == "" {
		return d
	}
	return s
}
