package main

import (
	"bytes"
	"context"
	"encoding/xml"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bcrisp4/bfeed/internal/core"
	"github.com/bcrisp4/bfeed/internal/store/sqlite"
)

func TestRunExportStdoutAndFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "feeds.db")
	t.Setenv("BFEED_DATABASE_PATH", path)
	t.Setenv("BFEED_BASE_URL", "")
	t.Setenv("BFEED_SCHED_FACTOR", "invalid") // Export needs no server settings.
	db, err := sqlite.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	store := sqlite.New(db)
	catID, err := store.CreateCategory(t.Context(), &core.Category{UserID: core.DefaultUserID, Title: "Tech"})
	if err != nil {
		t.Fatal(err)
	}
	fid, err := store.CreateFeed(t.Context(), &core.Feed{UserID: core.DefaultUserID, FeedURL: "https://a.test/atom", Title: "Original", CategoryID: &catID})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetFeedUserTitle(t.Context(), core.DefaultUserID, fid, "Renamed"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), "INSERT INTO users(id, username, created_at) VALUES (2, 'other', 0)"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateFeed(t.Context(), &core.Feed{UserID: 2, FeedURL: "https://other.test/private", Title: "Secret"}); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if code := runExport(nil, &stdout, &stderr); code != 0 {
		t.Fatalf("export exit %d: %s", code, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Errorf("unexpected stderr: %s", stderr.String())
	}
	if strings.Contains(stdout.String(), "Secret") || strings.Contains(stdout.String(), "other.test") {
		t.Fatalf("export includes another user's feed: %s", stdout.String())
	}
	var doc struct {
		Body struct {
			Outlines []struct {
				Text     string `xml:"text,attr"`
				Outlines []struct {
					Text   string `xml:"text,attr"`
					XMLURL string `xml:"xmlUrl,attr"`
				} `xml:"outline"`
			} `xml:"outline"`
		} `xml:"body"`
	}
	if err := xml.Unmarshal(stdout.Bytes(), &doc); err != nil {
		t.Fatalf("invalid OPML: %v\n%s", err, stdout.String())
	}
	if len(doc.Body.Outlines) != 1 || doc.Body.Outlines[0].Text != "Tech" || len(doc.Body.Outlines[0].Outlines) != 1 || doc.Body.Outlines[0].Outlines[0].Text != "Renamed" || doc.Body.Outlines[0].Outlines[0].XMLURL != "https://a.test/atom" {
		t.Fatalf("wrong export content: %s", stdout.String())
	}

	exported := bytes.Clone(stdout.Bytes())
	file := filepath.Join(t.TempDir(), "backup.opml")
	stdout.Reset()
	stderr.Reset()
	if code := runExport([]string{"-o", file}, &stdout, &stderr); code != 0 {
		t.Fatalf("file export exit %d: %s", code, stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("file export wrote stdout: %s", stdout.String())
	}
	contents, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(contents, exported) {
		t.Fatalf("file export differs from stdout: %s", contents)
	}
	info, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("export file permissions = %o, want private", info.Mode().Perm())
	}
	if err := os.Chmod(file, 0o644); err != nil {
		t.Fatal(err)
	}
	if code := runExport([]string{"-o", file}, &stdout, &stderr); code != 0 {
		t.Fatalf("replace export exit %d: %s", code, stderr.String())
	}
	info, err = os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("replacement export file permissions = %o, want private", info.Mode().Perm())
	}

	stderr.Reset()
	if code := runExport([]string{"-o", path}, &stdout, &stderr); code != 1 || stdout.Len() != 0 {
		t.Fatalf("database overwrite: code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "output path is the database") {
		t.Fatalf("database overwrite error: %s", stderr.String())
	}
}

func TestRunExportFailureDoesNotWriteStdout(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.db")
	t.Setenv("BFEED_DATABASE_PATH", path)
	var stdout, stderr bytes.Buffer
	if code := runExport(nil, &stdout, &stderr); code != 1 || stdout.Len() != 0 || stderr.Len() == 0 {
		t.Fatalf("failed export: code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("export created a new database: %v", err)
	}
	stderr.Reset()
	if code := runExport([]string{"extra"}, &stdout, &stderr); code != 2 || stdout.Len() != 0 {
		t.Fatalf("invalid args: code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}
