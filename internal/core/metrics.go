package core

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"time"
)

type PollResult string

const (
	PollSuccess     PollResult = "success"
	PollNotModified PollResult = "not_modified"
	PollFetchError  PollResult = "fetch_error"
	PollHTTPError   PollResult = "http_error"
	PollParseError  PollResult = "parse_error"
	PollStoreError  PollResult = "store_error"
	PollPanic       PollResult = "panic"
)

type ScrapeResult string

const (
	ScrapeSuccess      ScrapeResult = "success"
	ScrapeFetchError   ScrapeResult = "fetch_error"
	ScrapeHTTPError    ScrapeResult = "http_error"
	ScrapeExtractError ScrapeResult = "extract_error"
	ScrapeRetried      ScrapeResult = "retried"
	ScrapeFailed       ScrapeResult = "failed"
)

type ErrorComponent string

const (
	CompFeedPoll      ErrorComponent = "feed_poll"
	CompArticleScrape ErrorComponent = "article_scrape"
	CompDB            ErrorComponent = "db"
	CompHTTPServer    ErrorComponent = "http_server"
	CompImageProxy    ErrorComponent = "image_proxy"
)

type ErrorReason string

const (
	ReasonTimeout     ErrorReason = "timeout"
	ReasonDNS         ErrorReason = "dns"
	ReasonTLS         ErrorReason = "tls"
	ReasonHTTP4xx     ErrorReason = "http_4xx"
	ReasonHTTP5xx     ErrorReason = "http_5xx"
	ReasonRateLimited ErrorReason = "rate_limited"
	ReasonParse       ErrorReason = "parse"
	ReasonInternal    ErrorReason = "internal"
)

// AllPollResults enumerates every PollResult value. It is the single source of
// truth for the enum's members: observability's pre-registration (so
// bfeed_feed_polls_total's zero samples exist for every result from t=0) and
// its own lint test range over this slice rather than duplicating the list,
// so a new PollResult value can't silently be added to the type without also
// being wired into pre-registration (F9).
var AllPollResults = []PollResult{
	PollSuccess,
	PollNotModified,
	PollFetchError,
	PollHTTPError,
	PollParseError,
	PollStoreError,
	PollPanic,
}

// AllScrapeResults enumerates every ScrapeResult value. See AllPollResults.
var AllScrapeResults = []ScrapeResult{
	ScrapeSuccess,
	ScrapeFetchError,
	ScrapeHTTPError,
	ScrapeExtractError,
	ScrapeRetried,
	ScrapeFailed,
}

// classifyHTTPStatus buckets a non-200 HTTP response status into the closed
// reason enum. Shared by PollFeed's and ScrapeEntry's status ladders (F9) so
// the two classifications can't drift apart. 429 takes precedence over the
// generic 4xx bucket (a rate-limited response is a distinct, actionable
// reason). A status outside both the 4xx and 5xx ranges (e.g. a stray
// 201/204/206/300, or a Location-less 301) is not an HTTP client/server
// failure in the usual sense, so it buckets to internal rather than the
// misleading http_4xx (F2).
func classifyHTTPStatus(status int) ErrorReason {
	switch {
	case status == 429:
		return ReasonRateLimited
	case status >= 500:
		return ReasonHTTP5xx
	case status >= 400:
		return ReasonHTTP4xx
	default:
		return ReasonInternal
	}
}

// ctxShutdownCanceled reports whether err is context.Canceled AND ctx itself
// (the poll/scrape's own context) has already been cancelled — i.e. this
// fetch failure is a symptom of the process shutting down (or the request
// budget being torn down), not a genuine poll/scrape failure worth counting
// (F3). The ctx.Err() check matters because a fetch can also return
// context.Canceled from an unrelated *inner* cancellation while the caller's
// own ctx is still live; that case must still emit normally, so
// ClassifyFetchError's existing "internal" bucket for non-timeout cancels is
// left untouched — this is a metrics-emission gate only, not a
// reclassification.
func ctxShutdownCanceled(ctx context.Context, err error) bool {
	return ctx.Err() != nil && errors.Is(err, context.Canceled)
}

// Metrics is the observability port core services emit into. Implemented by
// the Prometheus adapter in internal/observability; NopMetrics is the default
// so services never nil-check. Label values are closed enums only — never
// feed/host/url/user values (unbounded label cardinality).
type Metrics interface {
	FeedPollDone(result PollResult)
	ObserveFeedPoll(d time.Duration)
	ScrapeDone(result ScrapeResult)
	ObserveArticleScrape(d time.Duration)
	ErrorObserved(c ErrorComponent, r ErrorReason)
	AddPollInflight(delta int)
	AddScrapeInflight(delta int)
	PollerTicked(t time.Time)
	ScraperTicked(t time.Time)
}

// NopMetrics is a no-op Metrics implementation — the default so services
// never have to nil-check their injected Metrics port.
type NopMetrics struct{}

func (NopMetrics) FeedPollDone(PollResult)                   {}
func (NopMetrics) ObserveFeedPoll(time.Duration)             {}
func (NopMetrics) ScrapeDone(ScrapeResult)                   {}
func (NopMetrics) ObserveArticleScrape(time.Duration)        {}
func (NopMetrics) ErrorObserved(ErrorComponent, ErrorReason) {}
func (NopMetrics) AddPollInflight(int)                       {}
func (NopMetrics) AddScrapeInflight(int)                     {}
func (NopMetrics) PollerTicked(time.Time)                    {}
func (NopMetrics) ScraperTicked(time.Time)                   {}

var _ Metrics = NopMetrics{}

// ClassifyFetchError buckets a Fetcher error into the closed reason enum.
// stdlib only — core must not import internal/fetch or any adapter package.
// Order is deliberate: timeout is checked before DNS, since a DNS lookup
// timeout (*net.DNSError with IsTimeout true) also satisfies net.Error's
// Timeout() — classifying it as a timeout rather than a DNS error is an
// acceptable, deterministic choice.
func ClassifyFetchError(err error) ErrorReason {
	if errors.Is(err, context.DeadlineExceeded) {
		return ReasonTimeout
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return ReasonTimeout
	}

	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return ReasonDNS
	}

	var certVerifyErr *tls.CertificateVerificationError
	var unknownAuthorityErr x509.UnknownAuthorityError
	var hostnameErr x509.HostnameError
	var certInvalidErr x509.CertificateInvalidError
	var recordHeaderErr tls.RecordHeaderError
	switch {
	case errors.As(err, &certVerifyErr),
		errors.As(err, &unknownAuthorityErr),
		errors.As(err, &hostnameErr),
		errors.As(err, &certInvalidErr),
		errors.As(err, &recordHeaderErr):
		return ReasonTLS
	}

	return ReasonInternal
}
