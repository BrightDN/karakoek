// Package session wraps gorilla/sessions for the guest and admin cookies.
package session

import (
	"net/http"
	"time"

	"github.com/gorilla/sessions"
)

const (
	GuestCookie = "karakoek_guest"
	AdminCookie = "karakoek_admin"
	maxAge      = 24 * 60 * 60
)

type Manager struct {
	store *sessions.CookieStore
}

func New(hashKey, blockKey []byte, secure bool) *Manager {
	s := sessions.NewCookieStore(hashKey, blockKey)
	s.Options = &sessions.Options{
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	}
	s.MaxAge(maxAge) // also bounds the securecookie timestamp check
	return &Manager{store: s}
}

// ---------- guest ----------

type Guest struct {
	Name          string
	Table         int
	PriorityUsed  bool
	LastRequestAt int64
	CSRF          string
}

func (m *Manager) Guest(r *http.Request) (Guest, bool) {
	sess, _ := m.store.Get(r, GuestCookie) // a bad cookie yields a fresh empty session
	name, _ := sess.Values["name"].(string)
	table, _ := sess.Values["table"].(int)
	if name == "" || table <= 0 {
		return Guest{}, false
	}
	g := Guest{Name: name, Table: table}
	g.PriorityUsed, _ = sess.Values["priority_used"].(bool)
	g.LastRequestAt, _ = sess.Values["last_request_at"].(int64)
	g.CSRF, _ = sess.Values["csrf"].(string)
	return g, true
}

func (m *Manager) SaveGuest(w http.ResponseWriter, r *http.Request, g Guest) error {
	sess, _ := m.store.Get(r, GuestCookie)
	sess.Values["name"] = g.Name
	sess.Values["table"] = g.Table
	sess.Values["priority_used"] = g.PriorityUsed
	sess.Values["last_request_at"] = g.LastRequestAt
	sess.Values["csrf"] = g.CSRF
	return sess.Save(r, w)
}

// AddFlash stores a one-shot message in the guest cookie.
func (m *Manager) AddFlash(w http.ResponseWriter, r *http.Request, msg string) {
	sess, _ := m.store.Get(r, GuestCookie)
	sess.AddFlash(msg)
	_ = sess.Save(r, w)
}

// Flash reads and clears the one-shot message.
func (m *Manager) Flash(w http.ResponseWriter, r *http.Request) string {
	sess, _ := m.store.Get(r, GuestCookie)
	f := sess.Flashes()
	if len(f) == 0 {
		return ""
	}
	_ = sess.Save(r, w)
	s, _ := f[0].(string)
	return s
}

// ---------- admin ----------

func (m *Manager) AdminCSRF(r *http.Request) (string, bool) {
	sess, _ := m.store.Get(r, AdminCookie)
	ok, _ := sess.Values["authenticated"].(bool)
	if !ok {
		return "", false
	}
	if at, _ := sess.Values["login_at"].(int64); at == 0 || time.Since(time.Unix(at, 0)) > maxAge*time.Second {
		return "", false
	}
	csrf, _ := sess.Values["csrf"].(string)
	return csrf, true
}

func (m *Manager) LoginAdmin(w http.ResponseWriter, r *http.Request, csrf string) error {
	sess, _ := m.store.Get(r, AdminCookie)
	sess.Values["authenticated"] = true
	sess.Values["login_at"] = time.Now().Unix()
	sess.Values["csrf"] = csrf
	return sess.Save(r, w)
}

func (m *Manager) LogoutAdmin(w http.ResponseWriter, r *http.Request) {
	sess, _ := m.store.Get(r, AdminCookie)
	sess.Values = map[interface{}]interface{}{}
	sess.Options.MaxAge = -1
	_ = sess.Save(r, w)
}
