package core

import (
	"log/slog"
	"runtime/debug"
)

// RecoverGuard is a deferred backstop for long-lived background goroutines that
// process untrusted feed/article content (the poller/scraper workers and the
// web background feed-op goroutines). net/http only recovers panics in request
// handlers, so a panic in any of these goroutines — e.g. a bug in the feed
// parser, readability extraction, or the sanitiser tripped by attacker-supplied
// input — would otherwise terminate the whole process.
//
// The graceful path is the recover inside the pipeline methods (PollFeed,
// ResolveAndIngest, ScrapeEntry), which turns a panic into a recorded,
// rescheduled error so a single bad feed/entry backs off instead of re-tripping
// every tick. RecoverGuard is the last line of defence: it keeps the daemon
// alive even if that path itself faults.
//
// Use as: defer core.RecoverGuard(log, "poll worker", "feed_id", id)
func RecoverGuard(log *slog.Logger, where string, kv ...any) {
	if r := recover(); r != nil {
		log.Error("recovered panic in "+where, append([]any{"panic", r, "stack", string(debug.Stack())}, kv...)...)
	}
}
