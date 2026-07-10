CREATE TABLE IF NOT EXISTS notes (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    raw_text TEXT NOT NULL,
    note_date TEXT,
    hash TEXT NOT NULL UNIQUE,
    enriched_at TEXT,
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS entries (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    note_id INTEGER NOT NULL REFERENCES notes(id) ON DELETE CASCADE,
    source_entry_id INTEGER REFERENCES entries(id) ON DELETE CASCADE,
    kind TEXT NOT NULL,
    chinese TEXT NOT NULL,
    chinese_raw TEXT NOT NULL,
    pinyin TEXT,
    english TEXT,
    suggested_cloze_word TEXT,
    grammar_note TEXT,
    typo_correction TEXT,
    hash TEXT NOT NULL UNIQUE,
    enriched_at TEXT,
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_entries_source ON entries(source_entry_id);
CREATE INDEX IF NOT EXISTS idx_entries_note ON entries(note_id);

CREATE TABLE IF NOT EXISTS cards (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    entry_id INTEGER NOT NULL REFERENCES entries(id) ON DELETE CASCADE,
    card_type TEXT NOT NULL,
    front TEXT NOT NULL,
    back TEXT NOT NULL,
    cloze_answer TEXT,
    hash TEXT NOT NULL UNIQUE,
    ease_factor REAL NOT NULL DEFAULT 2.5,
    interval_days INTEGER NOT NULL DEFAULT 0,
    repetitions INTEGER NOT NULL DEFAULT 0,
    due_at TEXT NOT NULL DEFAULT (datetime('now')),
    last_reviewed TEXT,
    lapses INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_cards_due ON cards(due_at);
CREATE INDEX IF NOT EXISTS idx_cards_entry ON cards(entry_id);

CREATE TABLE IF NOT EXISTS reviews (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    card_id INTEGER NOT NULL REFERENCES cards(id) ON DELETE CASCADE,
    rating INTEGER NOT NULL,
    prev_interval INTEGER NOT NULL,
    new_interval INTEGER NOT NULL,
    prev_ef REAL NOT NULL,
    new_ef REAL NOT NULL,
    reviewed_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_reviews_at ON reviews(reviewed_at);
CREATE INDEX IF NOT EXISTS idx_reviews_card ON reviews(card_id);

CREATE TABLE IF NOT EXISTS settings (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS chat_messages (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    role TEXT NOT NULL CHECK (role IN ('user', 'assistant')),
    content TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_chat_messages_id ON chat_messages(id);

-- One row per calendar day, populated on the first home-page visit each
-- day. Basic fields land immediately from the picked entry; the LLM-
-- generated example is filled in by a background goroutine.
CREATE TABLE IF NOT EXISTS word_of_day (
    date TEXT PRIMARY KEY,
    entry_id INTEGER,
    chinese TEXT NOT NULL,
    pinyin TEXT NOT NULL DEFAULT '',
    english TEXT NOT NULL,
    example_chinese TEXT,
    example_pinyin TEXT,
    example_english TEXT,
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);
