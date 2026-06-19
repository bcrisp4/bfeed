package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"

	"github.com/bcrisp4/bfeed/internal/config"
	"github.com/bcrisp4/bfeed/internal/store/sqlite"
)

var version = "dev" // overridden via -ldflags

func main() {
	cmd := "serve"
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}
	switch cmd {
	case "serve":
		os.Exit(runServe())
	case "migrate":
		os.Exit(runMigrate())
	case "healthcheck":
		os.Exit(runHealthcheck())
	case "version":
		fmt.Printf("bfeed %s\n", version)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q (serve|migrate|healthcheck|version)\n", cmd)
		os.Exit(2)
	}
}

func runMigrate() int {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	db, err := sqlite.Open(context.Background(), cfg.DatabasePath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	_ = db.Close()
	fmt.Println("migrations applied")
	return 0
}

func runHealthcheck() int {
	cfg, err := config.Load()
	if err != nil {
		return 1
	}
	_, port, err := net.SplitHostPort(cfg.ListenAddr)
	if err != nil {
		// Fall back: treat ListenAddr as ":port" style or bare addr.
		port = cfg.ListenAddr
	}
	resp, err := http.Get("http://127.0.0.1:" + port + "/healthz") //nolint:gosec
	if err != nil {
		return 1
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		return 1
	}
	return 0
}
