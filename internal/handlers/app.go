package handlers

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"html/template"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"karakoek/internal/config"
	"karakoek/internal/hub"
	"karakoek/internal/queue"
	"karakoek/internal/session"
	"karakoek/web"
)

type App struct {
	cfg   *config.Config
	pool  *pgxpool.Pool
	q     *queue.Service
	hub   *hub.Hub
	sess  *session.Manager
	tmpl  *template.Template
	login *limiter
}

func New(cfg *config.Config, pool *pgxpool.Pool, q *queue.Service, h *hub.Hub, s *session.Manager) (*App, error) {
	t, err := template.ParseFS(web.Templates, "templates/*.html")
	if err != nil {
		return nil, err
	}
	return &App{cfg: cfg, pool: pool, q: q, hub: h, sess: s, tmpl: t,
		login: newLimiter(5, 15*time.Minute)}, nil
}

// Routes builds the full handler tree.
func (a *App) Routes() http.Handler {
	mux := http.NewServeMux()

	static, _ := fs.Sub(web.Static, "static")
	mux.Handle("GET /static/", http.StripPrefix("/static/", cacheFor(http.FileServerFS(static), 3600)))

	mux.HandleFunc("GET /healthz", a.healthz)

	mux.HandleFunc("GET /{$}", a.guestPage)
	mux.HandleFunc("POST /join", a.join)
	mux.HandleFunc("POST /request", a.submitRequest)

	mux.HandleFunc("GET /admin/login", a.loginPage)
	mux.HandleFunc("POST /admin/login", a.loginSubmit)
	mux.Handle("POST /admin/logout", a.requireAdmin(false, a.requireCSRF(http.HandlerFunc(a.logout))))
	mux.Handle("GET /admin/player", a.requireAdmin(false, http.HandlerFunc(a.playerPage)))
	mux.Handle("GET /admin/queue", a.requireAdmin(false, http.HandlerFunc(a.queuePage)))
	mux.Handle("GET /ws", a.requireAdmin(true, http.HandlerFunc(a.hub.ServeWS)))
	mux.Handle("POST /admin/requests/{id}/link", a.requireAdmin(true, a.requireCSRF(http.HandlerFunc(a.setLink))))
	mux.Handle("DELETE /admin/requests/{id}", a.requireAdmin(true, a.requireCSRF(http.HandlerFunc(a.removeRequest))))

	return a.recoverer(a.securityHeaders(a.originCheck(mux)))
}

// ---------- helpers ----------

func (a *App) render(w http.ResponseWriter, status int, name string, data interface{}) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if err := a.tmpl.ExecuteTemplate(w, name, data); err != nil {
		log.Printf("template %s: %v", name, err)
	}
}

func (a *App) serverError(w http.ResponseWriter, err error) {
	log.Printf("error: %v", err)
	a.render(w, http.StatusInternalServerError, "error.html", nil)
}

func newToken() string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func tokenEqual(a, b string) bool {
	return a != "" && subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func cacheFor(next http.Handler, seconds int) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age="+itoa(seconds))
		next.ServeHTTP(w, r)
	})
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

func (a *App) clientIP(r *http.Request) string {
	if a.cfg.TrustProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			return strings.TrimSpace(parts[len(parts)-1])
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (a *App) healthz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := a.pool.Ping(ctx); err != nil {
		http.Error(w, "database unavailable", http.StatusServiceUnavailable)
		return
	}
	_, _ = w.Write([]byte("ok"))
}

// ---------- middleware ----------

func (a *App) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("panic: %v", rec)
				http.Error(w, "internal error", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func (a *App) securityHeaders(next http.Handler) http.Handler {
	csp := "default-src 'self'; " +
		"script-src 'self' https://www.youtube.com; " +
		"frame-src https://www.youtube.com https://www.youtube-nocookie.com; " +
		"img-src 'self' data: https://i.ytimg.com; " +
		"style-src 'self'; connect-src 'self' ws: wss:; " +
		"object-src 'none'; base-uri 'self'; form-action 'self'; frame-ancestors 'none'"
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", csp)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		h.Set("X-Frame-Options", "DENY")
		next.ServeHTTP(w, r)
	})
}

// originCheck rejects cross-site state-changing requests. SameSite=Lax cookies
// are the first line of defence; this is the second.
func (a *App) originCheck(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
			src := r.Header.Get("Origin")
			if src == "" {
				src = r.Header.Get("Referer")
			}
			if src != "" {
				u, err := url.Parse(src)
				if err != nil || u.Host != a.cfg.BaseURL.Host {
					http.Error(w, "forbidden", http.StatusForbidden)
					return
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

type ctxKey string

const csrfKey ctxKey = "csrf"

// requireAdmin: api=true answers 401, otherwise redirects to the login form.
func (a *App) requireAdmin(api bool, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		csrf, ok := a.sess.AdminCSRF(r)
		if !ok {
			if api {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			http.Redirect(w, r, "/admin/login?next="+url.QueryEscape(r.URL.Path), http.StatusFound)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), csrfKey, csrf)))
	})
}

func (a *App) requireCSRF(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		want, _ := r.Context().Value(csrfKey).(string)
		got := r.Header.Get("X-CSRF-Token")
		if got == "" {
			got = r.FormValue("csrf")
		}
		if !tokenEqual(want, got) {
			http.Error(w, "invalid CSRF token", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ---------- login rate limiter ----------

type limiter struct {
	mu     sync.Mutex
	max    int
	window time.Duration
	hits   map[string][]time.Time
}

func newLimiter(max int, window time.Duration) *limiter {
	return &limiter{max: max, window: window, hits: map[string][]time.Time{}}
}

func (l *limiter) prune(key string) []time.Time {
	cut := time.Now().Add(-l.window)
	kept := l.hits[key][:0]
	for _, t := range l.hits[key] {
		if t.After(cut) {
			kept = append(kept, t)
		}
	}
	if len(kept) == 0 {
		delete(l.hits, key)
	} else {
		l.hits[key] = kept
	}
	return kept
}

func (l *limiter) blocked(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.prune(key)) >= l.max
}

func (l *limiter) fail(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.prune(key)
	l.hits[key] = append(l.hits[key], time.Now())
}

func (l *limiter) reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.hits, key)
}
