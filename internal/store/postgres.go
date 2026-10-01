package store

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PGStore is the Postgres implementation. Used when DATABASE_URL is set.
type PGStore struct {
	pool *pgxpool.Pool
}

func NewPostgres(pool *pgxpool.Pool) *PGStore { return &PGStore{pool: pool} }

func (p *PGStore) CreateUser(email, hash string) (User, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	u := User{ID: uuid.NewString(), Email: email, PasswordHash: hash, CreatedAt: time.Now().UTC()}
	_, err := p.pool.Exec(context.Background(),
		`INSERT INTO users(id,email,password_hash) VALUES($1,$2,$3)`, u.ID, u.Email, u.PasswordHash)
	if err != nil {
		if strings.Contains(err.Error(), "duplicate") || strings.Contains(err.Error(), "unique") {
			return User{}, ErrConflict
		}
		return User{}, err
	}
	return u, nil
}

func (p *PGStore) FindUserByEmail(email string) (User, error) {
	var u User
	err := p.pool.QueryRow(context.Background(),
		`SELECT id,email,password_hash,created_at FROM users WHERE email=$1`, strings.ToLower(strings.TrimSpace(email))).
		Scan(&u.ID, &u.Email, &u.PasswordHash, &u.CreatedAt)
	if err != nil {
		return User{}, ErrNotFound
	}
	return u, nil
}

func (p *PGStore) FindUserByID(id string) (User, error) {
	var u User
	err := p.pool.QueryRow(context.Background(),
		`SELECT id,email,password_hash,created_at FROM users WHERE id=$1::uuid`, id).
		Scan(&u.ID, &u.Email, &u.PasswordHash, &u.CreatedAt)
	if err != nil {
		return User{}, ErrNotFound
	}
	return u, nil
}

func (p *PGStore) SaveRefreshToken(userID, tokenHash string, expiresAt time.Time) error {
	_, err := p.pool.Exec(context.Background(),
		`INSERT INTO refresh_tokens(user_id,token_hash,expires_at) VALUES($1::uuid,$2,$3)`, userID, tokenHash, expiresAt)
	return err
}

func (p *PGStore) FindUserIDByRefreshHash(tokenHash string) (string, time.Time, bool, error) {
	var uid string
	var exp time.Time
	var revoked *time.Time
	err := p.pool.QueryRow(context.Background(),
		`SELECT user_id::text,expires_at,revoked_at FROM refresh_tokens WHERE token_hash=$1`, tokenHash).
		Scan(&uid, &exp, &revoked)
	if err != nil {
		return "", time.Time{}, false, ErrNotFound
	}
	return uid, exp, revoked != nil, nil
}

func (p *PGStore) RevokeRefreshToken(tokenHash string) error {
	_, err := p.pool.Exec(context.Background(),
		`UPDATE refresh_tokens SET revoked_at=now() WHERE token_hash=$1`, tokenHash)
	return err
}

func (p *PGStore) RevokeAllUserTokens(userID string) error {
	_, err := p.pool.Exec(context.Background(),
		`UPDATE refresh_tokens SET revoked_at=now() WHERE user_id=$1::uuid`, userID)
	return err
}

func scanTask(scan func(...any) error) (Task, error) {
	var t Task
	var due *time.Time
	var del *time.Time
	var id, uid string
	err := scan(&id, &uid, &t.Title, &t.Description, &t.Status, &t.Priority, &due, &t.Position, &t.Version, &t.UpdatedAt, &del)
	if err != nil {
		return Task{}, err
	}
	t.ID, t.UserID, t.DueDate, t.DeletedAt = id, uid, due, del
	return t, nil
}

const taskCols = `id::text,user_id::text,title,description,status,priority,due_date,position,version,updated_at,deleted_at`

func (p *PGStore) CreateTask(t Task) (Task, error) {
	if t.ID == "" {
		t.ID = uuid.NewString()
	}
	t.Status = normStatusPG(t.Status)
	ctx := context.Background()
	_, err := p.pool.Exec(ctx,
		`INSERT INTO tasks(id,user_id,title,description,status,priority,due_date,position) VALUES($1::uuid,$2::uuid,$3,$4,$5,$6,$7,$8)`,
		t.ID, t.UserID, t.Title, t.Description, t.Status, t.Priority, t.DueDate, t.Position)
	if err != nil {
		return Task{}, err
	}
	return p.GetTask(t.UserID, t.ID)
}

func (p *PGStore) ListTasks(userID, status, query string, limit, offset int) ([]Task, error) {
	if limit <= 0 {
		limit = 50
	}
	ctx := context.Background()
	rows, err := p.pool.Query(ctx, `SELECT `+taskCols+` FROM tasks
		WHERE user_id=$1::uuid AND deleted_at IS NULL
		AND ($2='' OR $2='all' OR status=$2)
		AND ($3='' OR title ILIKE '%'||$3||'%' OR description ILIKE '%'||$3||'%')
		ORDER BY position ASC, updated_at DESC LIMIT $4 OFFSET $5`, userID, status, query, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Task
	for rows.Next() {
		t, err := scanTask(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	if out == nil {
		out = []Task{}
	}
	return out, nil
}

func (p *PGStore) ListTasksSince(userID string, since time.Time) ([]Task, error) {
	rows, err := p.pool.Query(context.Background(),
		`SELECT `+taskCols+` FROM tasks WHERE user_id=$1::uuid AND updated_at>$2 ORDER BY updated_at ASC`, userID, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Task
	for rows.Next() {
		t, err := scanTask(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	if out == nil {
		out = []Task{}
	}
	return out, nil
}

func (p *PGStore) GetTask(userID, id string) (Task, error) {
	var t Task
	t, err := scanTask(p.pool.QueryRow(context.Background(),
		`SELECT `+taskCols+` FROM tasks WHERE id=$1::uuid AND user_id=$2::uuid AND deleted_at IS NULL`, id, userID).Scan)
	if err != nil {
		return Task{}, ErrNotFound
	}
	return t, nil
}

func (p *PGStore) UpdateTask(userID, id string, in TaskInput) (Task, error) {
	status := in.Status
	if status == "" {
		status = "todo"
	}
	status = normStatusPG(status)
	_, err := p.pool.Exec(context.Background(),
		`UPDATE tasks SET title=COALESCE(NULLIF($3,''),title), description=$4, status=$5,
		 priority=$6, due_date=$7, position=$8, version=version+1, updated_at=now()
		 WHERE id=$1::uuid AND user_id=$2::uuid AND deleted_at IS NULL`,
		id, userID, in.Title, in.Description, status, in.Priority, in.DueDate, in.Position)
	if err != nil {
		return Task{}, err
	}
	return p.GetTask(userID, id)
}

func (p *PGStore) DeleteTask(userID, id string) error {
	ct, err := p.pool.Exec(context.Background(),
		`UPDATE tasks SET deleted_at=now(), updated_at=now(), version=version+1
		 WHERE id=$1::uuid AND user_id=$2::uuid AND deleted_at IS NULL`, id, userID)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (p *PGStore) UpsertSync(userID string, in TaskInput, deleted bool) (Task, bool, error) {
	ctx := context.Background()
	id := in.ID
	if id == "" {
		id = uuid.NewString()
	}
	clientTS := time.Now().UTC()
	if in.UpdatedAt != nil {
		clientTS = *in.UpdatedAt
	}
	var existingUpdated time.Time
	err := p.pool.QueryRow(ctx, `SELECT updated_at FROM tasks WHERE id=$1::uuid AND user_id=$2::uuid`, id, userID).Scan(&existingUpdated)
	if err != nil { // insert
		status := normStatusPG(orDefaultPG(in.Status, "todo"))
		title := in.Title
		if title == "" {
			title = "(untitled)"
		}
		var del *time.Time
		if deleted {
			n := time.Now().UTC()
			del = &n
		}
		_, err := p.pool.Exec(ctx,
			`INSERT INTO tasks(id,user_id,title,description,status,priority,due_date,position,deleted_at)
			 VALUES($1::uuid,$2::uuid,$3,$4,$5,$6,$7,$8,$9)`,
			id, userID, title, in.Description, status, in.Priority, in.DueDate, in.Position, del)
		if err != nil {
			return Task{}, false, err
		}
		t, err := scanTask(p.pool.QueryRow(ctx, `SELECT `+taskCols+` FROM tasks WHERE id=$1::uuid`, id).Scan)
		return t, true, err
	}
	if !clientTS.After(existingUpdated) {
		t, err := scanTask(p.pool.QueryRow(ctx, `SELECT `+taskCols+` FROM tasks WHERE id=$1::uuid`, id).Scan)
		return t, false, err // conflict, server kept
	}
	status := normStatusPG(orDefaultPG(in.Status, "todo"))
	if deleted {
		_, err = p.pool.Exec(ctx, `UPDATE tasks SET deleted_at=now(), updated_at=now(), version=version+1 WHERE id=$1::uuid`, id)
	} else {
		_, err = p.pool.Exec(ctx, `UPDATE tasks SET title=$2,description=$3,status=$4,priority=$5,due_date=$6,position=$7,
			deleted_at=NULL,version=version+1,updated_at=now() WHERE id=$1::uuid`,
			id, orDefaultPG(in.Title, "(untitled)"), in.Description, status, in.Priority, in.DueDate, in.Position)
	}
	if err != nil {
		return Task{}, false, err
	}
	t, err := scanTask(p.pool.QueryRow(ctx, `SELECT `+taskCols+` FROM tasks WHERE id=$1::uuid`, id).Scan)
	return t, true, err
}

func normStatusPG(s string) string {
	switch s {
	case "doing", "done":
		return s
	default:
		return "todo"
	}
}

func orDefaultPG(s, d string) string {
	if s == "" {
		return d
	}
	return s
}
