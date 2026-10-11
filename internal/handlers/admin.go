package handlers

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/BrightDN/karakoek/internal/youtube"

	"github.com/BrightDN/karakoek/internal/queue"
	"golang.org/x/crypto/bcrypt"
)

type adminView struct {
	CSRF  string
	Error string
	Next  string
}

// safeNext only allows local paths to prevent open redirects.
func safeNext(n string) string {
	if strings.HasPrefix(n, "/") && !strings.HasPrefix(n, "//") && !strings.Contains(n, "\\") {
		return n
	}
	return "/admin/queue"
}

func (a *App) loginPage(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.sess.AdminCSRF(r); ok {
		http.Redirect(w, r, safeNext(r.URL.Query().Get("next")), http.StatusFound)
		return
	}
	a.render(w, http.StatusOK, "login.html", adminView{Next: safeNext(r.URL.Query().Get("next"))})
}

func (a *App) loginSubmit(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	ip := a.clientIP(r)
	next := safeNext(r.FormValue("next"))
	if a.login.blocked(ip) {
		a.render(w, http.StatusTooManyRequests, "login.html", adminView{Next: next, Error: "Too many attempts. Try again later."})
		return
	}

	userOK := subtle.ConstantTimeCompare([]byte(r.FormValue("username")), []byte(a.cfg.AdminUsername)) == 1
	passOK := bcrypt.CompareHashAndPassword(a.cfg.AdminPasswordHash, []byte(r.FormValue("password"))) == nil
	if !userOK || !passOK {
		a.login.fail(ip)
		time.Sleep(750 * time.Millisecond)
		a.render(w, http.StatusUnauthorized, "login.html", adminView{Next: next, Error: "Invalid username or password."})
		return
	}
	a.login.reset(ip)
	if err := a.sess.LoginAdmin(w, r, newToken()); err != nil {
		a.serverError(w, err)
		return
	}
	http.Redirect(w, r, next, http.StatusSeeOther)
}

func (a *App) logout(w http.ResponseWriter, r *http.Request) {
	a.sess.LogoutAdmin(w, r)
	http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
}

func (a *App) playerPage(w http.ResponseWriter, r *http.Request) {
	csrf, _ := a.sess.AdminCSRF(r)
	a.render(w, http.StatusOK, "player.html", adminView{CSRF: csrf})
}

func (a *App) queuePage(w http.ResponseWriter, r *http.Request) {
	csrf, _ := a.sess.AdminCSRF(r)
	a.render(w, http.StatusOK, "queue.html", adminView{CSRF: csrf})
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (a *App) setLink(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}
	link := strings.TrimSpace(r.FormValue("link"))
	videoID, err := youtube.ParseVideoID(link)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Not a recognised YouTube link"})
		return
	}
	if err := a.q.SetLink(r.Context(), id, link, videoID); err != nil {
		if errors.Is(err, queue.ErrNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "Request not found"})
			return
		}
		a.jsonError(w, err)
		return
	}
	a.hub.BroadcastQueue()
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (a *App) removeRequest(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}
	if err := a.q.Remove(r.Context(), id); err != nil {
		if errors.Is(err, queue.ErrNotFound) {
			a.hub.BroadcastQueue() // already gone: just resync
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "Request not found"})
			return
		}
		a.jsonError(w, err)
		return
	}
	a.hub.BroadcastQueue()
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (a *App) jsonError(w http.ResponseWriter, err error) {
	writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Server error"})
	log.Printf("error: %v", err)
}
