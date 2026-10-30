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

-- Intro/credits markers (imported from Plex or detected later).
CREATE TABLE IF NOT EXISTS markers (
    item_id INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
    type    TEXT NOT NULL,                   -- intro | credits
    start   REAL NOT NULL,                   -- seconds
    "end"   REAL NOT NULL,
    PRIMARY KEY (item_id, type, start)
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
