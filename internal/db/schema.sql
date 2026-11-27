CREATE TABLE IF NOT EXISTS settings (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS users (
    id            INTEGER PRIMARY KEY,
    username      TEXT NOT NULL UNIQUE COLLATE NOCASE,
    password_hash TEXT NOT NULL,
    is_admin      INTEGER NOT NULL DEFAULT 0,
    created_at    INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS sessions (
    token      TEXT PRIMARY KEY,
    user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at INTEGER NOT NULL,
    expires_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS libraries (
    id           INTEGER PRIMARY KEY,
    name         TEXT NOT NULL,
    kind         TEXT NOT NULL CHECK (kind IN ('movies', 'shows')),
    paths        TEXT NOT NULL,          -- JSON array of root paths
    trust_dir_mtime INTEGER NOT NULL DEFAULT 0,
    created_at   INTEGER NOT NULL,
    last_scan_at INTEGER
);

-- Directory mtimes remembered between scans so unchanged directories can be
-- skipped without listing them (opt-in per library, see trust_dir_mtime).
CREATE TABLE IF NOT EXISTS directories (
    library_id INTEGER NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    path       TEXT NOT NULL,
    parent     TEXT,
    mtime      INTEGER NOT NULL,
    PRIMARY KEY (library_id, path)
);
CREATE INDEX IF NOT EXISTS directories_parent ON directories(library_id, parent);

-- One table for every browsable thing: movie, show, season, episode.
CREATE TABLE IF NOT EXISTS items (
    id             INTEGER PRIMARY KEY,
    library_id     INTEGER NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    kind           TEXT NOT NULL CHECK (kind IN ('movie', 'show', 'season', 'episode')),
    guid           TEXT NOT NULL UNIQUE,
    parent_id      INTEGER REFERENCES items(id) ON DELETE CASCADE,
    title          TEXT NOT NULL,
    sort_title     TEXT NOT NULL,
    original_title TEXT,
    year           INTEGER,
    season_number  INTEGER,
    episode_number INTEGER,
    overview       TEXT,
    tagline        TEXT,
    genres         TEXT,                 -- JSON array
    rating         REAL,
    runtime        INTEGER,              -- minutes
    release_date   TEXT,
    tmdb_id        INTEGER,
    imdb_id        TEXT,
    poster         TEXT,                 -- TMDB image path
    backdrop       TEXT,
    logo           TEXT,
    meta_state     TEXT NOT NULL DEFAULT 'pending', -- pending|matched|unmatched|error
    meta_at        INTEGER,
    added_at       INTEGER NOT NULL,
    updated_at     INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS items_lib_kind_sort  ON items(library_id, kind, sort_title);
CREATE INDEX IF NOT EXISTS items_lib_kind_added ON items(library_id, kind, added_at DESC);
CREATE INDEX IF NOT EXISTS items_kind_added     ON items(kind, added_at DESC);
CREATE INDEX IF NOT EXISTS items_parent         ON items(parent_id, season_number, episode_number);
CREATE INDEX IF NOT EXISTS items_meta_state     ON items(meta_state, kind);

CREATE TABLE IF NOT EXISTS media_files (
    id          INTEGER PRIMARY KEY,
    item_id     INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
    library_id  INTEGER NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    path        TEXT NOT NULL UNIQUE,
    size        INTEGER NOT NULL,
    mtime       INTEGER NOT NULL,
    container   TEXT,
    video_codec TEXT,
    audio_codec TEXT,
    width       INTEGER,
    height      INTEGER,
    duration    REAL,                    -- seconds
    bitrate     INTEGER,
    streams     TEXT,                    -- JSON summary of audio/subtitle streams
    probed_at   INTEGER,
    probe_error TEXT
);
CREATE INDEX IF NOT EXISTS media_files_item    ON media_files(item_id);
CREATE INDEX IF NOT EXISTS media_files_lib     ON media_files(library_id);
CREATE INDEX IF NOT EXISTS media_files_unprobed ON media_files(probed_at) WHERE probed_at IS NULL;

CREATE TABLE IF NOT EXISTS watch_state (
    user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    item_id    INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
    position   REAL NOT NULL DEFAULT 0,
    duration   REAL NOT NULL DEFAULT 0,
    watched    INTEGER NOT NULL DEFAULT 0,
    play_count INTEGER NOT NULL DEFAULT 0,
    updated_at INTEGER NOT NULL,
    PRIMARY KEY (user_id, item_id)
);
CREATE INDEX IF NOT EXISTS watch_state_recent ON watch_state(user_id, updated_at DESC);

CREATE VIRTUAL TABLE IF NOT EXISTS items_fts USING fts5(
    title, original_title, overview,
    content='items', content_rowid='id',
    tokenize='unicode61 remove_diacritics 2',
    prefix='2 3'
);

CREATE TRIGGER IF NOT EXISTS items_fts_ai AFTER INSERT ON items BEGIN
    INSERT INTO items_fts(rowid, title, original_title, overview)
    VALUES (new.id, new.title, new.original_title, new.overview);
END;
CREATE TRIGGER IF NOT EXISTS items_fts_ad AFTER DELETE ON items BEGIN
    INSERT INTO items_fts(items_fts, rowid, title, original_title, overview)
    VALUES ('delete', old.id, old.title, old.original_title, old.overview);
END;
CREATE TRIGGER IF NOT EXISTS items_fts_au AFTER UPDATE OF title, original_title, overview ON items BEGIN
    INSERT INTO items_fts(items_fts, rowid, title, original_title, overview)
    VALUES ('delete', old.id, old.title, old.original_title, old.overview);
    INSERT INTO items_fts(rowid, title, original_title, overview)
    VALUES (new.id, new.title, new.original_title, new.overview);
END;

-- Films and shows only, for search. items_fts is nine parts episodes, so a
-- search for common words ("about time") had to rank tens of thousands of
-- episode summaries before it could answer; this index is small enough to
-- stay in memory. Filled by DB.BuildTitleIndex on libraries that predate it.
CREATE VIRTUAL TABLE IF NOT EXISTS titles_fts USING fts5(
    title, original_title, overview,
    content='items', content_rowid='id',
    tokenize='unicode61 remove_diacritics 2',
    prefix='2 3'
);

CREATE TRIGGER IF NOT EXISTS titles_fts_ai AFTER INSERT ON items WHEN new.kind IN ('movie', 'show') BEGIN
    INSERT INTO titles_fts(rowid, title, original_title, overview)
    VALUES (new.id, new.title, new.original_title, new.overview);
END;
CREATE TRIGGER IF NOT EXISTS titles_fts_ad AFTER DELETE ON items WHEN old.kind IN ('movie', 'show') BEGIN
    INSERT INTO titles_fts(titles_fts, rowid, title, original_title, overview)
    VALUES ('delete', old.id, old.title, old.original_title, old.overview);
END;
CREATE TRIGGER IF NOT EXISTS titles_fts_au AFTER UPDATE OF title, original_title, overview ON items WHEN old.kind IN ('movie', 'show') BEGIN
    INSERT INTO titles_fts(titles_fts, rowid, title, original_title, overview)
    VALUES ('delete', old.id, old.title, old.original_title, old.overview);
    INSERT INTO titles_fts(rowid, title, original_title, overview)
    VALUES (new.id, new.title, new.original_title, new.overview);
END;

-- Intro/credits/recap markers (imported from Plex or detected on first play).
CREATE TABLE IF NOT EXISTS markers (
    item_id INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
    type    TEXT NOT NULL,                   -- intro | credits | recap
    start   REAL NOT NULL,                   -- seconds
    "end"   REAL NOT NULL,
    PRIMARY KEY (item_id, type, start)
);

-- Audio fingerprints for marker detection (internal/detect). part is head
-- (first minutes), tail (last minutes) or full; offset is the start time.
CREATE TABLE IF NOT EXISTS fingerprints (
    file_id    INTEGER NOT NULL REFERENCES media_files(id) ON DELETE CASCADE,
    part       TEXT NOT NULL,
    offset     REAL NOT NULL,
    size       INTEGER NOT NULL,                -- media file size it was made from
    data       BLOB NOT NULL,
    created_at INTEGER NOT NULL,
    PRIMARY KEY (file_id, part)
);

-- Which items marker detection has looked at.
CREATE TABLE IF NOT EXISTS detect_runs (
    item_id INTEGER PRIMARY KEY REFERENCES items(id) ON DELETE CASCADE,
    version INTEGER NOT NULL,
    done_at INTEGER NOT NULL,
    note    TEXT
);

-- Link to the source item on a Plex server, for imports and marker lookups.
CREATE TABLE IF NOT EXISTS plex_links (
    item_id    INTEGER PRIMARY KEY REFERENCES items(id) ON DELETE CASCADE,
    rating_key TEXT NOT NULL,
    markers_at INTEGER
);
CREATE INDEX IF NOT EXISTS plex_links_key ON plex_links(rating_key);

-- Library sharing: users without all_libraries only see the libraries listed here.
CREATE TABLE IF NOT EXISTS user_libraries (
    user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    library_id INTEGER NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    PRIMARY KEY (user_id, library_id)
);

CREATE TABLE IF NOT EXISTS invites (
    token         TEXT PRIMARY KEY,
    created_by    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    note          TEXT,
    all_libraries INTEGER NOT NULL DEFAULT 0,
    libraries     TEXT NOT NULL DEFAULT '[]', -- JSON array of library ids
    created_at    INTEGER NOT NULL,
    expires_at    INTEGER NOT NULL,
    used_by       INTEGER REFERENCES users(id) ON DELETE SET NULL,
    used_at       INTEGER
);

-- Per-user display choices for each library: hidden from the menu (tab bar,
-- sidebar) and/or from Home's rows. Access control stays in user_libraries.
CREATE TABLE IF NOT EXISTS user_library_prefs (
    user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    library_id INTEGER NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    hide_nav   INTEGER NOT NULL DEFAULT 0,
    hide_home  INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (user_id, library_id)
);

-- Each user's watchlist.
CREATE TABLE IF NOT EXISTS watchlist (
    user_id  INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    item_id  INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
    added_at INTEGER NOT NULL,
    PRIMARY KEY (user_id, item_id)
);

-- "Remove from Continue Watching": the item (a film or episode) or show stays
-- off the row until it's watched again after hidden_at.
CREATE TABLE IF NOT EXISTS continue_hidden (
    user_id   INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    item_id   INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
    hidden_at INTEGER NOT NULL,
    PRIMARY KEY (user_id, item_id)
);

-- "Remove from suggestions": films and shows a user (or profile) doesn't want
-- suggested. They stay off Home's suggestion rows, the featured titles at the
-- top, Recently Added and the screensaver; libraries and search still show
-- them.
CREATE TABLE IF NOT EXISTS suggest_hidden (
    user_id   INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    item_id   INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
    hidden_at INTEGER NOT NULL,
    PRIMARY KEY (user_id, item_id)
);

-- Every completed play, for Plexodus Wrapped and "Because you watched".
-- watch_state only keeps the latest state per item; this keeps each view.
-- source: plexodus (played here), plex (Plex play history), state (seeded
-- from watch_state once, for plays from before this table existed).
CREATE TABLE IF NOT EXISTS watch_history (
    id         INTEGER PRIMARY KEY,
    user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    item_id    INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
    watched_at INTEGER NOT NULL,
    seconds    REAL NOT NULL DEFAULT 0,       -- how long the item is; 0 = unknown
    source     TEXT NOT NULL DEFAULT 'plexodus'
);
CREATE INDEX IF NOT EXISTS watch_history_user ON watch_history(user_id, watched_at);
CREATE INDEX IF NOT EXISTS watch_history_item ON watch_history(user_id, item_id, watched_at);

-- Lip sync correction for one file: how much later (ms, negative = earlier)
-- its sound should play. Set from the player's "Audio sync" control and
-- applied on every device, on top of each device's own offset.
CREATE TABLE IF NOT EXISTS av_offsets (
    file_id    INTEGER PRIMARY KEY REFERENCES media_files(id) ON DELETE CASCADE,
    offset_ms  INTEGER NOT NULL,
    updated_by INTEGER,
    updated_at INTEGER NOT NULL
);

-- Film collections (franchises) from TMDB: "The Lord of the Rings Collection".
-- items.collection_id points here; 0 means TMDB says the film is in none.
CREATE TABLE IF NOT EXISTS collections (
    id         INTEGER PRIMARY KEY,      -- TMDB collection id
    name       TEXT NOT NULL,
    sort_name  TEXT NOT NULL,
    poster     TEXT,
    backdrop   TEXT,
    updated_at INTEGER NOT NULL
);

-- What is in each Plex collection (collections.source = 'plex'), in Plex's
-- order. TMDB franchises use items.collection_id instead.
CREATE TABLE IF NOT EXISTS collection_items (
    collection_id INTEGER NOT NULL REFERENCES collections(id) ON DELETE CASCADE,
    item_id       INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
    position      INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (collection_id, item_id)
) WITHOUT ROWID;
CREATE INDEX IF NOT EXISTS collection_items_item ON collection_items(item_id);

-- Every season and episode TMDB lists for a show (metadata.Guide as JSON),
-- fetched when someone opens the show, so its page can show missing and
-- upcoming episodes. Never fetched in bulk.
CREATE TABLE IF NOT EXISTS tmdb_guides (
    tmdb_id    INTEGER PRIMARY KEY,
    data       TEXT NOT NULL,
    fetched_at INTEGER NOT NULL
);

-- Scene companion: notes Claude wrote for one stretch of a title
-- (chunk = start second / 600). grounded = written with the subtitles.
CREATE TABLE IF NOT EXISTS companion_notes (
    item_id    INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
    chunk      INTEGER NOT NULL,
    grounded   INTEGER NOT NULL DEFAULT 0,
    notes      TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    PRIMARY KEY (item_id, chunk)
) WITHOUT ROWID;

-- Mood profile of every film (internal/mood): thirteen scores, 0–100. src 1 =
-- worked out ahead of time for the film's TMDB id, 0 = estimated from its
-- genres. sig is what the row was made from, so a re-match or new genres
-- refresh it.
CREATE TABLE IF NOT EXISTS item_moods (
    item_id  INTEGER PRIMARY KEY REFERENCES items(id) ON DELETE CASCADE,
    v        INTEGER NOT NULL,
    a        INTEGER NOT NULL,
    dark     INTEGER NOT NULL,
    humour   INTEGER NOT NULL,
    complex  INTEGER NOT NULL,
    pace     INTEGER NOT NULL,
    weight   INTEGER NOT NULL,
    comfort  INTEGER NOT NULL,
    violence INTEGER NOT NULL,
    scary    INTEGER NOT NULL,
    sex      INTEGER NOT NULL,
    family   INTEGER NOT NULL,
    romance  INTEGER NOT NULL,
    src      INTEGER NOT NULL DEFAULT 0,
    sig      TEXT NOT NULL
);

-- "Surprise me" and "Help me choose" memory, per viewer and film: when it was
-- last suggested (it then rests for 30 days), how many times it was skipped
-- (three hides it for a year), until when it is hidden, and a like (1) or
-- dislike (-1) that shapes later picks.
CREATE TABLE IF NOT EXISTS pick_state (
    user_id   INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    item_id   INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
    shown_at  INTEGER NOT NULL DEFAULT 0,
    strikes   INTEGER NOT NULL DEFAULT 0,
    ban_until INTEGER NOT NULL DEFAULT 0,
    ban_why   TEXT,                          -- notint | skips
    vote      INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (user_id, item_id)
) WITHOUT ROWID;

-- Genres a viewer never wants to see (mode 1) or insists on (mode 2). Applied
-- to their library pages and every pick.
CREATE TABLE IF NOT EXISTS pick_genres (
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    genre   TEXT NOT NULL,
    mode    INTEGER NOT NULL,
    PRIMARY KEY (user_id, genre)
) WITHOUT ROWID;
