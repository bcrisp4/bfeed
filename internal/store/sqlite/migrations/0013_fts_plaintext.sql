-- +goose Up
-- Index a plain-text projection of content/summary instead of the raw sanitised
-- HTML, so full-text search matches visible words rather than tag names, attribute
-- names, and URLs (audit B12). content_text/summary_text are populated going forward
-- by the store write path (core.PlainText) and back-filled for pre-existing rows by
-- the Go migrate step (pure SQL cannot strip tags).
ALTER TABLE entries ADD COLUMN content_text TEXT NOT NULL DEFAULT '';
ALTER TABLE entries ADD COLUMN summary_text TEXT NOT NULL DEFAULT '';

DROP TRIGGER entries_au;
DROP TRIGGER entries_ad;
DROP TRIGGER entries_ai;
DROP TABLE entries_fts;

-- Rebuilt over the text columns. No 'rebuild' here: pre-existing rows still have
-- empty text at this point; the Go backfill populates them and rebuilds once.
CREATE VIRTUAL TABLE entries_fts USING fts5(
  title, content_text, summary_text,
  content='entries', content_rowid='id',
  tokenize='unicode61'
);

-- +goose StatementBegin
CREATE TRIGGER entries_ai AFTER INSERT ON entries BEGIN
  INSERT INTO entries_fts(rowid, title, content_text, summary_text)
  VALUES (new.id, new.title, new.content_text, new.summary_text);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER entries_ad AFTER DELETE ON entries BEGIN
  INSERT INTO entries_fts(entries_fts, rowid, title, content_text, summary_text)
  VALUES('delete', old.id, old.title, old.content_text, old.summary_text);
END;
-- +goose StatementEnd

-- NOTE: the AFTER UPDATE trigger (entries_au) is intentionally NOT created here. It
-- issues an external-content 'delete' using the row's OLD indexed values; if it fired
-- during the Go backfill's UPDATEs against the freshly-emptied index, that delete
-- would reference tokens the index never held and corrupt it. The backfill creates
-- entries_au (with the plain-text columns) only AFTER it has populated the columns
-- and rebuilt the index. See backfillSearchText in db.go.

-- +goose Down
DROP TRIGGER entries_au;
DROP TRIGGER entries_ad;
DROP TRIGGER entries_ai;
DROP TABLE entries_fts;

ALTER TABLE entries DROP COLUMN content_text;
ALTER TABLE entries DROP COLUMN summary_text;

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

-- +goose StatementBegin
CREATE TRIGGER entries_au AFTER UPDATE OF title, content, summary ON entries BEGIN
  INSERT INTO entries_fts(entries_fts, rowid, title, content, summary)
  VALUES('delete', old.id, old.title, old.content, old.summary);
  INSERT INTO entries_fts(rowid, title, content, summary)
  VALUES (new.id, new.title, new.content, new.summary);
END;
-- +goose StatementEnd

INSERT INTO entries_fts(entries_fts) VALUES('rebuild');
