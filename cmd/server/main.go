package main

import (
	"context"
	"log"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	chiMW "github.com/go-chi/chi/v5/middleware"

	"todolist/internal/auth"
	"todolist/internal/config"
	"todolist/internal/db"
	"todolist/internal/store"
	"todolist/internal/tasks"
	"todolist/internal/web"
)

func main() {
	cfg := config.Load()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pool, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("db connect: %v", err)
	}
	if pool != nil {
		defer pool.Close()
		if err := db.RunMigrations(context.Background(), pool); err != nil {
			log.Fatalf("migrate: %v", err)
		}
		log.Println("postgres connected + migrated")
	} else {
		log.Println("DATABASE_URL empty -> memory store (dev mode)")
	}

	var st store.Store = store.NewMemory()
	if pool != nil {
		st = store.NewPostgres(pool)
	}

	authSvc := auth.NewService(st, cfg.JWTSecret, cfg.AccessTTL, cfg.RefreshTTL)
	taskH := tasks.New(st)
	webH, err := web.New("web/templates")
	if err != nil {
		log.Fatalf("templates: %v", err)
	}

	r := chi.NewRouter()
	r.Use(chiMW.Logger, chiMW.Recoverer)

	// static + PWA
	r.Handle("/static/*", http.StripPrefix("/static/", http.FileServer(http.Dir("web/static"))))

	// pages
	r.Mount("/", webH.Routes())

	// JSON API
	r.Mount("/api/auth", authSvc.Routes())
	r.Mount("/api/tasks", taskH.Routes(authSvc.Middleware))

	r.Get("/health", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok")) })

	addr := ":" + cfg.Port
	log.Printf("listening on %s", addr)
	log.Fatal(http.ListenAndServe(addr, r))
}
