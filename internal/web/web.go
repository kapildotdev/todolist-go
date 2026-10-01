package web

import (
	"html/template"
	"net/http"

	"github.com/go-chi/chi/v5"
)

type Handler struct {
	tpl *template.Template
}

func New(templateDir string) (*Handler, error) {
	tpl, err := template.ParseGlob(templateDir + "/*.html")
	if err != nil {
		return nil, err
	}
	return &Handler{tpl: tpl}, nil
}

func (h *Handler) Routes() chi.Router {
	r := chi.NewRouter()
	r.Get("/", h.landing)
	r.Get("/login", h.loginPage)
	r.Get("/app", h.appPage)
	return r
}

func (h *Handler) landing(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/app", http.StatusFound)
}

func (h *Handler) loginPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html")
	_ = h.tpl.ExecuteTemplate(w, "login.html", nil)
}

func (h *Handler) appPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html")
	_ = h.tpl.ExecuteTemplate(w, "app.html", nil)
}
