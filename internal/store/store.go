package store

import (
	"errors"
	"time"
)

var ErrNotFound = errors.New("not found")
var ErrConflict = errors.New("conflict")
var ErrUnauthorized = errors.New("unauthorized")

type User struct {
	ID           string    `json:"id"`
	Email        string    `json:"email"`
	PasswordHash string    `json:"-"`
	CreatedAt    time.Time `json:"created_at"`
}

type Task struct {
	ID          string     `json:"id"`
	UserID      string     `json:"user_id"`
	Title       string     `json:"title"`
	Description string     `json:"description"`
	Status      string     `json:"status"` // todo | doing | done
	Priority    int        `json:"priority"`
	DueDate     *time.Time `json:"due_date,omitempty"`
	Position    int        `json:"position"`
	Version     int        `json:"version"`
	UpdatedAt   time.Time  `json:"updated_at"`
	DeletedAt   *time.Time `json:"deleted_at,omitempty"`
}

// TaskInput is what clients send (id optional -> server generates).
type TaskInput struct {
	ID          string     `json:"id,omitempty"`
	Title       string     `json:"title"`
	Description string     `json:"description"`
	Status      string     `json:"status"`
	Priority    int        `json:"priority"`
	DueDate     *time.Time `json:"due_date"`
	Position    int        `json:"position"`
	UpdatedAt   *time.Time `json:"updated_at,omitempty"` // client clock for LWW
}

type Store interface {
	// users
	CreateUser(email, hash string) (User, error)
	FindUserByEmail(email string) (User, error)
	FindUserByID(id string) (User, error)
	// refresh tokens (hash stored)
	SaveRefreshToken(userID, tokenHash string, expiresAt time.Time) error
	FindUserIDByRefreshHash(tokenHash string) (userID string, expiresAt time.Time, revoked bool, err error)
	RevokeRefreshToken(tokenHash string) error
	RevokeAllUserTokens(userID string) error
	// tasks
	CreateTask(t Task) (Task, error)
	ListTasks(userID, status, query string, limit, offset int) ([]Task, error)
	ListTasksSince(userID string, since time.Time) ([]Task, error)
	GetTask(userID, id string) (Task, error)
	UpdateTask(userID, id string, in TaskInput) (Task, error)
	DeleteTask(userID, id string) error
	// Upsert used by /sync with LWW: returns (task, applied bool, conflict bool)
	UpsertSync(userID string, in TaskInput, deleted bool) (Task, bool, error)
}
