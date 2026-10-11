package handlers

import (
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"karakoek/internal/session"
	"karakoek/internal/youtube"
)

const (
	cooldown     = 10 * time.Minute
	maxNameLen   = 40
	maxSongLen   = 100
	maxArtistLen = 100
)

type guestView struct {
	State        string // "notice" | "form"
	Name         string
	Table        int
	CSRF         string
	Code         string
	AskName      bool
	PriorityUsed bool
	Cooldown     int // seconds remaining
	Flash        string
	Error        string
	NameError    string
	Song, Artist string
	Link         string
	Priority     bool
}

func (a *App) cooldownLeft(g session.Guest) int {
	if g.LastRequestAt == 0 {
		return 0
	}
	left := time.Until(time.Unix(g.LastRequestAt, 0).Add(cooldown))
	if left <= 0 {
		return 0
	}
	return int((left + time.Second - 1) / time.Second)
}

func (a *App) guestPage(w http.ResponseWriter, r *http.Request) {
	code := strings.TrimSpace(r.URL.Query().Get("code"))
	table, codeOK := a.cfg.TableCodes[code]

	g, hasCookie := a.sess.Guest(r)
	if hasCookie {
		if codeOK && table != g.Table {
			g.Table = table
			if err := a.sess.SaveGuest(w, r, g); err != nil {
				a.serverError(w, err)
				return
			}
		}
		a.render(w, http.StatusOK, "guest.html", guestView{
			State: "form", Name: g.Name, Table: g.Table, CSRF: g.CSRF,
			PriorityUsed: g.PriorityUsed, Cooldown: a.cooldownLeft(g),
			Flash: a.sess.Flash(w, r),
		})
		return
	}
	if !codeOK {
		a.render(w, http.StatusOK, "guest.html", guestView{State: "notice"})
		return
	}
	// Valid code, no cookie yet: form behind a mandatory name dialog.
	a.render(w, http.StatusOK, "guest.html", guestView{
		State: "form", Table: table, Code: code, AskName: true,
	})
}

func (a *App) join(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	code := strings.TrimSpace(r.FormValue("code"))
	name := strings.TrimSpace(r.FormValue("name"))

	table, ok := a.cfg.TableCodes[code]
	if !ok {
		a.render(w, http.StatusForbidden, "guest.html", guestView{State: "notice"})
		return
	}
	if n := utf8.RuneCountInString(name); n < 1 || n > maxNameLen {
		a.render(w, http.StatusBadRequest, "guest.html", guestView{
			State: "form", Table: table, Code: code, AskName: true,
			NameError: "Please enter a name (1–40 characters).",
		})
		return
	}
	g := session.Guest{Name: name, Table: table, CSRF: newToken()}
	if err := a.sess.SaveGuest(w, r, g); err != nil {
		a.serverError(w, err)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (a *App) submitRequest(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	g, ok := a.sess.Guest(r)
	if !ok {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if !tokenEqual(g.CSRF, r.FormValue("csrf")) {
		http.Error(w, "invalid CSRF token", http.StatusForbidden)
		return
	}

	view := guestView{
		State: "form", Name: g.Name, Table: g.Table, CSRF: g.CSRF, PriorityUsed: g.PriorityUsed,
		Song:     strings.TrimSpace(r.FormValue("song")),
		Artist:   strings.TrimSpace(r.FormValue("artist")),
		Link:     strings.TrimSpace(r.FormValue("link")),
		Priority: r.FormValue("priority") != "",
	}

	if left := a.cooldownLeft(g); left > 0 {
		view.Cooldown = left
		view.Error = "Please wait before requesting again."
		w.Header().Set("Retry-After", itoa(left))
		a.render(w, http.StatusTooManyRequests, "guest.html", view)
		return
	}

	fail := func(msg string) {
		view.Error = msg
		a.render(w, http.StatusBadRequest, "guest.html", view)
	}
	if n := utf8.RuneCountInString(view.Song); n < 1 || n > maxSongLen {
		fail("Please enter a song name (up to 100 characters).")
		return
	}
	if n := utf8.RuneCountInString(view.Artist); n < 1 || n > maxArtistLen {
		fail("Please enter an artist name (up to 100 characters).")
		return
	}
	videoID := ""
	if view.Link != "" {
		id, err := youtube.ParseVideoID(view.Link)
		if err != nil {
			fail("That doesn't look like a YouTube link.")
			return
		}
		videoID = id
	}

	priority := view.Priority && !g.PriorityUsed // an already-used priority is ignored

	if _, err := a.q.Add(r.Context(), g.Name, g.Table, priority, view.Song, view.Artist, view.Link, videoID); err != nil {
		a.serverError(w, err)
		return
	}

	g.LastRequestAt = time.Now().Unix()
	if priority {
		g.PriorityUsed = true
	}
	if err := a.sess.SaveGuest(w, r, g); err != nil {
		a.serverError(w, err)
		return
	}
	a.sess.AddFlash(w, r, "Your request is in the queue!")
	a.hub.BroadcastQueue()
	http.Redirect(w, r, "/", http.StatusSeeOther)
}
