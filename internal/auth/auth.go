package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"

	"todolist/internal/store"
)

type Service struct {
	store      store.Store
	secret     string
	accessTTL  time.Duration
	refreshTTL time.Duration
}

func NewService(s store.Store, secret string, accessTTL, refreshTTL time.Duration) *Service {
	return &Service{store: s, secret: secret, accessTTL: accessTTL, refreshTTL: refreshTTL}
}

type ctxKey string

const userKey ctxKey = "user_id"

// UserID returns authenticated user id from context.
func UserID(r *http.Request) (string, bool) {
	v := r.Context().Value(userKey)
	s, ok := v.(string)
	return s, ok && s != ""
}

func hashToken(t string) string {
	h := sha256.Sum256([]byte(t))
	return hex.EncodeToString(h[:])
}

func (s *Service) issueAccess(userID string) (string, error) {
	claims := jwt.MapClaims{"sub": userID, "exp": time.Now().Add(s.accessTTL).Unix(), "iat": time.Now().Unix()}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return tok.SignedString([]byte(s.secret))
}

func (s *Service) parseAccess(token string) (string, error) {
	t, err := jwt.Parse(token, func(t *jwt.Token) (any, error) {
		if t.Method.Alg() != jwt.SigningMethodHS256.Alg() {
			return nil, jwt.ErrSignatureInvalid
		}
		return []byte(s.secret), nil
	})
	if err != nil || !t.Valid {
		return "", jwt.ErrSignatureInvalid
	}
	claims, ok := t.Claims.(jwt.MapClaims)
	if !ok {
		return "", jwt.ErrInvalidKey
	}
	sub, _ := claims["sub"].(string)
	if sub == "" {
		return "", jwt.ErrInvalidKey
	}
	return sub, nil
}

// Middleware verifies `Authorization: Bearer <jwt>`.
// On failure with HTMX/API it returns 401 JSON (client then tries /refresh).
func (s *Service) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := r.Header.Get("Authorization")
		token := strings.TrimPrefix(h, "Bearer ")
		if h == "" || token == h {
			writeJSON(w, 401, map[string]string{"error": "missing token"})
			return
		}
		uid, err := s.parseAccess(token)
		if err != nil {
			writeJSON(w, 401, map[string]string{"error": "invalid or expired token"})
			return
		}
		ctx := context.WithValue(r.Context(), userKey, uid)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (s *Service) Routes() chi.Router {
	r := chi.NewRouter()
	r.Post("/register", s.handleRegister)
	r.Post("/login", s.handleLogin)
	r.Post("/refresh", s.handleRefresh)
	r.Post("/logout", s.handleLogout)
	r.With(s.Middleware).Get("/me", s.handleMe)
	return r
}

type creds struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func (s *Service) handleRegister(w http.ResponseWriter, r *http.Request) {
	var c creds
	if err := json.NewDecoder(r.Body).Decode(&c); err != nil {
		writeJSON(w, 400, map[string]string{"error": "invalid json"})
		return
	}
	c.Email = strings.ToLower(strings.TrimSpace(c.Email))
	if !strings.Contains(c.Email, "@") || len(c.Password) < 8 {
		writeJSON(w, 400, map[string]string{"error": "email invalid or password < 8 chars"})
		return
	}
	hash, _ := bcrypt.GenerateFromPassword([]byte(c.Password), bcrypt.DefaultCost)
	u, err := s.store.CreateUser(c.Email, string(hash))
	if err == store.ErrConflict {
		writeJSON(w, 409, map[string]string{"error": "email already registered"})
		return
	}
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "server error"})
		return
	}
	access := s.attachSession(w, u.ID)
	writeJSON(w, 201, map[string]any{"id": u.ID, "email": u.Email, "access_token": access, "token_type": "Bearer"})
}

func (s *Service) handleLogin(w http.ResponseWriter, r *http.Request) {
	var c creds
	if err := json.NewDecoder(r.Body).Decode(&c); err != nil {
		writeJSON(w, 400, map[string]string{"error": "invalid json"})
		return
	}
	u, err := s.store.FindUserByEmail(strings.ToLower(strings.TrimSpace(c.Email)))
	if err != nil {
		writeJSON(w, 401, map[string]string{"error": "invalid credentials"})
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(c.Password)) != nil {
		writeJSON(w, 401, map[string]string{"error": "invalid credentials"})
		return
	}
	access := s.attachSession(w, u.ID)
	writeJSON(w, 200, map[string]any{"id": u.ID, "email": u.Email, "access_token": access, "token_type": "Bearer"})
}

// attachSession issues access token (returned + X-Access-Token header) + refresh token (HttpOnly cookie + hashed store).
func (s *Service) attachSession(w http.ResponseWriter, userID string) string {
	access, _ := s.issueAccess(userID)
	refresh := uuid.NewString() + "." + uuid.NewString()
	_ = s.store.SaveRefreshToken(userID, hashToken(refresh), time.Now().Add(s.refreshTTL))
	http.SetCookie(w, &http.Cookie{
		Name: "refresh_token", Value: refresh, Path: "/",
		HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: false, // Secure=true behind HTTPS (Render sets it); keep false for localhost
		Expires: time.Now().Add(s.refreshTTL),
	})
	w.Header().Set("X-Access-Token", access)
	return access
}

func (s *Service) tokenBody(userID string) map[string]any {
	access, _ := s.issueAccess(userID)
	return map[string]any{"access_token": access, "token_type": "Bearer"}
}

func (s *Service) handleRefresh(w http.ResponseWriter, r *http.Request) {
	c, err := r.Cookie("refresh_token")
	if err != nil {
		writeJSON(w, 401, map[string]string{"error": "no refresh cookie"})
		return
	}
	uid, exp, revoked, err := s.store.FindUserIDByRefreshHash(hashToken(c.Value))
	if err != nil || revoked || time.Now().After(exp) {
		writeJSON(w, 401, map[string]string{"error": "refresh expired"})
		return
	}
	// rotation: revoke old, issue new pair
	_ = s.store.RevokeRefreshToken(hashToken(c.Value))
	s.attachSession(w, uid)
	body := s.tokenBody(uid)
	writeJSON(w, 200, body)
}

func (s *Service) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie("refresh_token"); err == nil {
		_ = s.store.RevokeRefreshToken(hashToken(c.Value))
	}
	http.SetCookie(w, &http.Cookie{Name: "refresh_token", Value: "", Path: "/", MaxAge: -1, HttpOnly: true})
	writeJSON(w, 200, map[string]string{"status": "logged out"})
}

func (s *Service) handleMe(w http.ResponseWriter, r *http.Request) {
	uid, _ := UserID(r)
	u, err := s.store.FindUserByID(uid)
	if err != nil {
		writeJSON(w, 404, map[string]string{"error": "user not found"})
		return
	}
	writeJSON(w, 200, map[string]any{"id": u.ID, "email": u.Email})
}

// WithLoginJSON wraps attachSession to include access token in body (used by register/login).
// We hook by re-reading cookie: simpler—handlers above call attachSession then tokenBody.
// Patch: re-wrap responses to include token.
func init() {}

// writeJSON helper
func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
