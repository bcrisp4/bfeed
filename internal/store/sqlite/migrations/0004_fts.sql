-- +goose Up
CREATE VIRTUAL TABLE entries_fts USING fts5(
  title, content, summary,
  content='entries', content_rowid='id',
  tokenize='unicode61'
);

-- +goose StatementBegin
CREATE TRIGGER entries_ai AFTER INSERT ON entries BEGIN
  INSERT INTO entries_fts(rowid, title, content, summary)
  VALUES (new.id, new.title, new.content, new.summary);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER entries_ad AFTER DELETE ON entries BEGIN
  INSERT INTO entries_fts(entries_fts, rowid, title, content, summary)
  VALUES('delete', old.id, old.title, old.content, old.summary);
END;
-- +goose StatementEnd

-- AFTER UPDATE OF: re-index only when an INDEXED column changes. read/unread and
-- star toggles (the hot path) change status/starred/read_at, never the indexed
-- text, so they must not churn the FTS index.
-- +goose StatementBegin
CREATE TRIGGER entries_au AFTER UPDATE OF title, content, summary ON entries BEGIN
  INSERT INTO entries_fts(entries_fts, rowid, title, content, summary)
  VALUES('delete', old.id, old.title, old.content, old.summary);
  INSERT INTO entries_fts(rowid, title, content, summary)
  VALUES (new.id, new.title, new.content, new.summary);
END;
-- +goose StatementEnd

-- MANDATORY: index every entry that predates this migration.
INSERT INTO entries_fts(entries_fts) VALUES('rebuild');

-- +goose Down
DROP TRIGGER entries_au;
DROP TRIGGER entries_ad;
DROP TRIGGER entries_ai;
DROP TABLE entries_fts;
