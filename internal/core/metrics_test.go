package core_test

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/url"
	"testing"

	"github.com/bcrisp4/bfeed/internal/core"
)

// stubTimeoutErr is a minimal net.Error whose Timeout() reports true, used to
// simulate a timeout that isn't a *net.DNSError (e.g. a raw dial timeout).
type stubTimeoutErr struct{}

func (stubTimeoutErr) Error() string   { return "stub timeout" }
func (stubTimeoutErr) Timeout() bool   { return true }
func (stubTimeoutErr) Temporary() bool { return true }

var _ net.Error = stubTimeoutErr{}

func TestClassifyFetchError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want core.ErrorReason
	}{
		{
			name: "context deadline exceeded wrapped",
			err:  fmt.Errorf("http get: %w", context.DeadlineExceeded),
			want: core.ReasonTimeout,
		},
		{
			name: "net.Error Timeout() true wrapped in url.Error",
			err: fmt.Errorf("http get: %w", &url.Error{
				Op: "Get", URL: "https://example.com", Err: stubTimeoutErr{},
			}),
			want: core.ReasonTimeout,
		},
		{
			name: "dns error wrapped in url.Error",
			err: fmt.Errorf("http get: %w", &url.Error{
				Op: "Get", URL: "https://example.com",
				Err: &net.DNSError{Err: "no such host", Name: "example.invalid", IsNotFound: true},
			}),
			want: core.ReasonDNS,
		},
		{
			name: "x509 unknown authority wrapped",
			err:  fmt.Errorf("http get: %w", x509.UnknownAuthorityError{}),
			want: core.ReasonTLS,
		},
		{
			name: "x509 hostname error wrapped",
			err:  fmt.Errorf("http get: %w", x509.HostnameError{}),
			want: core.ReasonTLS,
		},
		{
			name: "x509 certificate invalid wrapped",
			err:  fmt.Errorf("http get: %w", x509.CertificateInvalidError{}),
			want: core.ReasonTLS,
		},
		{
			name: "tls record header error wrapped",
			err:  fmt.Errorf("http get: %w", tls.RecordHeaderError{Msg: "bad record"}),
			want: core.ReasonTLS,
		},
		{
			name: "tls certificate verification error wrapped",
			err:  fmt.Errorf("http get: %w", &tls.CertificateVerificationError{Err: errors.New("boom")}),
			want: core.ReasonTLS,
		},
		{
			name: "opaque error classifies internal",
			err:  errors.New("something went wrong"),
			want: core.ReasonInternal,
		},
		{
			name: "nil error classifies internal",
			err:  nil,
			want: core.ReasonInternal,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := core.ClassifyFetchError(tt.err)
			if got != tt.want {
				t.Fatalf("ClassifyFetchError(%v) = %q, want %q", tt.err, got, tt.want)
			}
		})
	}
}
