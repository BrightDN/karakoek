CREATE TYPE request_status AS ENUM ('queued', 'loaded', 'playing', 'finished');

CREATE TABLE song_requests (
  id            BIGSERIAL PRIMARY KEY,
  requester     TEXT NOT NULL,
  table_number  INT NOT NULL,
  priority      BOOLEAN NOT NULL DEFAULT FALSE,
  song_name     TEXT NOT NULL,
  artist_name   TEXT NOT NULL,
  link          TEXT,
  video_id      TEXT,
  status        request_status NOT NULL DEFAULT 'queued',
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  finished_at   TIMESTAMPTZ
);

CREATE INDEX idx_song_requests_queue
  ON song_requests (status, priority DESC, created_at, id);

-- At most one current (loaded or playing) song at any time.
CREATE UNIQUE INDEX one_current_song
  ON song_requests ((TRUE))
  WHERE status IN ('loaded', 'playing');
