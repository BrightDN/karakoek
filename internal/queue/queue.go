// Package queue is the only code that touches song_requests.
package queue

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrNotFound = errors.New("request not found")
	ErrBusy     = errors.New("another song is already loaded")
	ErrNoLink   = errors.New("request has no valid link")
)

type Request struct {
	ID        int64     `json:"id"`
	Requester string    `json:"requester"`
	Table     int       `json:"table"`
	Priority  bool      `json:"priority"`
	Song      string    `json:"song"`
	Artist    string    `json:"artist"`
	Link      string    `json:"link"`
	VideoID   string    `json:"videoId"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"createdAt"`
}

type Service struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Service { return &Service{pool: pool} }

const cols = `id, requester, table_number, priority, song_name, artist_name,
	COALESCE(link,''), COALESCE(video_id,''), status::text, created_at`

func scan(row pgx.Row) (Request, error) {
	var r Request
	err := row.Scan(&r.ID, &r.Requester, &r.Table, &r.Priority, &r.Song, &r.Artist,
		&r.Link, &r.VideoID, &r.Status, &r.CreatedAt)
	return r, err
}

// Add inserts a request. link/videoID may be empty.
func (s *Service) Add(ctx context.Context, requester string, table int, priority bool, song, artist, link, videoID string) (int64, error) {
	var id int64
	err := s.pool.QueryRow(ctx, `
		INSERT INTO song_requests (requester, table_number, priority, song_name, artist_name, link, video_id)
		VALUES ($1,$2,$3,$4,$5,NULLIF($6,''),NULLIF($7,''))
		RETURNING id`, requester, table, priority, song, artist, link, videoID).Scan(&id)
	return id, err
}

// Queued returns waiting requests: priority first, then arrival order.
func (s *Service) Queued(ctx context.Context) ([]Request, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+cols+` FROM song_requests
		WHERE status='queued' ORDER BY priority DESC, created_at ASC, id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Request{}
	for rows.Next() {
		r, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Current returns the loaded/playing request, or nil.
func (s *Service) Current(ctx context.Context) (*Request, error) {
	r, err := scan(s.pool.QueryRow(ctx, `SELECT `+cols+` FROM song_requests
		WHERE status IN ('loaded','playing') LIMIT 1`))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}

func (s *Service) Get(ctx context.Context, id int64) (*Request, error) {
	r, err := scan(s.pool.QueryRow(ctx, `SELECT `+cols+` FROM song_requests WHERE id=$1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// SetLink stores an admin-supplied link on a queued request.
func (s *Service) SetLink(ctx context.Context, id int64, link, videoID string) error {
	tag, err := s.pool.Exec(ctx, `UPDATE song_requests SET link=$2, video_id=$3
		WHERE id=$1 AND status='queued'`, id, link, videoID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// Remove deletes a queued request (troll cleanup).
func (s *Service) Remove(ctx context.Context, id int64) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM song_requests WHERE id=$1 AND status='queued'`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// Load marks a queued request as loaded and returns it.
func (s *Service) Load(ctx context.Context, id int64) (*Request, error) {
	r, err := scan(s.pool.QueryRow(ctx, `UPDATE song_requests SET status='loaded'
		WHERE id=$1 AND status='queued' AND video_id IS NOT NULL
		RETURNING `+cols, id))
	if err == nil {
		return &r, nil
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return nil, ErrBusy
	}
	if errors.Is(err, pgx.ErrNoRows) {
		if cur, _ := s.Get(ctx, id); cur != nil && cur.Status == "queued" {
			return nil, ErrNoLink
		}
		return nil, ErrNotFound
	}
	return nil, err
}

// MarkPlaying moves the current song to playing. Returns the current request or nil.
func (s *Service) MarkPlaying(ctx context.Context) (*Request, error) {
	return s.updateCurrent(ctx, `UPDATE song_requests SET status='playing' WHERE status IN ('loaded','playing') RETURNING `+cols)
}

// Finish stamps the current song finished.
func (s *Service) Finish(ctx context.Context) (*Request, error) {
	return s.updateCurrent(ctx, `UPDATE song_requests SET status='finished', finished_at=now() WHERE status IN ('loaded','playing') RETURNING `+cols)
}

// Undo returns the current song to the queue.
func (s *Service) Undo(ctx context.Context) (*Request, error) {
	return s.updateCurrent(ctx, `UPDATE song_requests SET status='queued' WHERE status IN ('loaded','playing') RETURNING `+cols)
}

// ResetPlaying turns a restored 'playing' song back into 'loaded' (cued, paused).
func (s *Service) ResetPlaying(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, `UPDATE song_requests SET status='loaded' WHERE status='playing'`)
	return err
}

func (s *Service) updateCurrent(ctx context.Context, q string) (*Request, error) {
	r, err := scan(s.pool.QueryRow(ctx, q))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// Counts returns unfinished requests (for the startup log).
func (s *Service) Unfinished(ctx context.Context) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `SELECT count(*) FROM song_requests WHERE status<>'finished'`).Scan(&n)
	return n, err
}
