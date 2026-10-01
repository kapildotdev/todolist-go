# Todo List Manager — Go (Chi) + HTMX + Postgres + Offline Sync

Learning project covering auth, middleware, JWT sessions, CRUD, PWA offline, autosave/manual save, and free-tier deploy.

## Stack
- Backend: Go + `chi` (stdlib-friendly), `pgx`, `golang-jwt`, `bcrypt`
- Frontend: HTMX CDN + vanilla JS (IndexedDB outbox) + Service Worker PWA
- DB: Postgres (Neon free). Falls back to in-memory store when `DATABASE_URL` is empty.

## Run locally (no DB needed)
```bash
cp .env.example .env
go run ./cmd/server        # -> http://localhost:8080/app
```

## Run with Postgres
```bash
docker run -e POSTGRES_PASSWORD=pass -p 5432:5432 postgres:16
export DATABASE_URL=postgres://postgres:pass@localhost:5432/postgres?sslmode=disable
go run ./cmd/server
```

## Key endpoints
- Pages: `GET /login`, `GET /app`, PWA: `/static/sw.js`, `/static/manifest.webmanifest`
- Auth: `POST /api/auth/register|login|refresh|logout`, `GET /api/auth/me`
- Tasks: `GET /api/tasks?status=&q=&limit=&offset=&since=`, `POST /api/tasks`, `PATCH /api/tasks/{id}`, `DELETE /api/tasks/{id}`
- Sync (offline replay, LWW): `POST /api/tasks/sync {upserts[], deletes[], last_pulled_at}`

## Auth design
- Access JWT 15m in `sessionStorage` (memory), sent as `Bearer`. Refresh token opaque UUID in HttpOnly cookie, sha256-hashed in DB, rotated on `/refresh`.
- `auth.Middleware` validates JWT, injects `user_id` into context. Client auto-refreshes on 401.

## Offline design
- Service Worker cache-first shell; IndexedDB `tasks` + `outbox` (Dexie-free, raw IndexedDB ~80 lines).
- Writes always go local first + append outbox. `flushOutbox()` POSTs `/sync`, then pulls delta `?since=`.
- Autosave: 1.5s debounce toggle. Manual: `Save Now (n)` + `Cmd/Ctrl+S`.
- Conflicts: Last-Write-Wins by `updated_at`; loser shown in `#conflicts` banner.

## Deploy (Render + Neon, both free)
1. Create Neon Postgres, copy pooled `DATABASE_URL`.
2. Push repo, create Render Web Service (Docker), set `DATABASE_URL`, `JWT_SECRET`.
3. `render.yaml` included. Migrations run on boot (`migrations/001_init.sql`).

## Try the flows
1. Register -> Add task -> toggle done -> Reload (online CRUD).
2. DevTools > Network > Offline -> add/edit tasks (badge `● n unsaved`) -> Go online -> auto `Save`/sync.
3. Open two tabs, edit same task, sync both -> newer `updated_at` wins.
4. Uncheck autosave -> edits pile up -> `Save Now`.

## What to learn next (suggested order)
1. `sqlc` for type-safe queries, `golang-migrate` for versioned migrations.
2. Rate-limit login (`chi` middleware + `golang.org/x/time/rate`).
3. Playwright offline E2E test.
4. Share lists (RBAC `list_members` table) — system design: authz beyond authn.
5. SSE `/api/events` for multi-tab live updates.
