package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/bcrisp4/bfeed/internal/config"
	"github.com/bcrisp4/bfeed/internal/core"
	"github.com/bcrisp4/bfeed/internal/store/sqlite"
)

func runExport(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("export", flag.ContinueOnError)
	flags.SetOutput(stderr)
	path := flags.String("o", "", "write OPML to file (default: stdout)")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "usage: bfeed export [-o file.opml]")
		return 2
	}

	cfg := config.LoadMinimal()
	dbInfo, err := os.Stat(cfg.DatabasePath)
	if err != nil {
		fmt.Fprintln(stderr, "export:", err)
		return 1
	}
	if *path != "" {
		if fileInfo, err := os.Stat(*path); err == nil && os.SameFile(dbInfo, fileInfo) {
			fmt.Fprintln(stderr, "export: output path is the database")
			return 1
		}
	}
	ctx := context.Background()
	db, err := sqlite.Open(ctx, cfg.DatabasePath)
	if err != nil {
		fmt.Fprintln(stderr, "export:", err)
		return 1
	}
	defer func() { _ = db.Close() }()
	store := sqlite.New(db)
	feeds, err := store.ListFeeds(ctx, core.DefaultUserID)
	if err != nil {
		fmt.Fprintln(stderr, "export feeds:", err)
		return 1
	}
	categories, err := store.ListCategories(ctx, core.DefaultUserID)
	if err != nil {
		fmt.Fprintln(stderr, "export categories:", err)
		return 1
	}
	data, err := core.MarshalOPML(feeds, categories)
	if err != nil {
		fmt.Fprintln(stderr, "export OPML:", err)
		return 1
	}
	if *path != "" {
		err = writeExportFile(*path, data)
	} else {
		_, err = stdout.Write(data)
	}
	if err != nil {
		fmt.Fprintln(stderr, "write OPML:", err)
		return 1
	}
	return 0
}

func writeExportFile(path string, data []byte) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".bfeed-export-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(file.Name()) }()
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}
