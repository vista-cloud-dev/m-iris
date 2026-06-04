package atelier

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// maxResponse caps a single response body (a routine, even a large one, is well
// under this); it bounds memory against a misbehaving or wrong endpoint.
const maxResponse = 64 << 20 // 64 MiB

// Config holds everything needed to reach an Atelier endpoint. Values are
// resolved upstream (flags > env) and passed in explicitly.
type Config struct {
	BaseURL    string // e.g. https://host:52773/api/atelier/v1/
	Namespace  string // IRIS namespace
	Token      string // OAuth2/bearer token (app auth; wins over basic auth)
	User       string // basic-auth user (optional when using a token or mTLS)
	Password   string // basic-auth password
	CAFile     string // PEM bundle for in-boundary TLS (optional)
	ClientCert string // client cert (PEM) for mutual TLS (optional)
	ClientKey  string // client key (PEM) for mutual TLS (optional)
	Timeout    time.Duration
}

// Client is an Atelier v1 read client for a single namespace.
type Client struct {
	base      *url.URL
	namespace string
	token     string
	user      string
	password  string
	hc        *http.Client
}

// New builds a Client, wiring TLS (custom CA pool / client cert) from Config.
// It does not contact the server.
func New(cfg Config) (*Client, error) {
	if cfg.BaseURL == "" {
		return nil, errors.New("atelier: base URL is required")
	}
	if cfg.Namespace == "" {
		return nil, errors.New("atelier: namespace is required")
	}
	base, err := url.Parse(cfg.BaseURL)
	if err != nil {
		return nil, fmt.Errorf("atelier: invalid base URL %q: %w", cfg.BaseURL, err)
	}
	if base.Scheme == "" || base.Host == "" {
		return nil, fmt.Errorf("atelier: base URL %q must be absolute (scheme://host)", cfg.BaseURL)
	}
	// A trailing slash makes the base a clean prefix for path joins.
	if !strings.HasSuffix(base.Path, "/") {
		base.Path += "/"
	}

	tlsCfg := &tls.Config{MinVersion: tls.VersionTLS12}
	if cfg.CAFile != "" {
		pem, err := os.ReadFile(cfg.CAFile)
		if err != nil {
			return nil, fmt.Errorf("atelier: read CA file: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("atelier: no certificates found in CA file %q", cfg.CAFile)
		}
		tlsCfg.RootCAs = pool
	}
	if cfg.ClientCert != "" || cfg.ClientKey != "" {
		if cfg.ClientCert == "" || cfg.ClientKey == "" {
			return nil, errors.New("atelier: --client-cert and --client-key must be provided together")
		}
		cert, err := tls.LoadX509KeyPair(cfg.ClientCert, cfg.ClientKey)
		if err != nil {
			return nil, fmt.Errorf("atelier: load client certificate: %w", err)
		}
		tlsCfg.Certificates = []tls.Certificate{cert}
	}

	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = 60 * time.Second
	}
	return &Client{
		base:      base,
		namespace: cfg.Namespace,
		token:     cfg.Token,
		user:      cfg.User,
		password:  cfg.Password,
		hc: &http.Client{
			Timeout: timeout,
			Transport: &http.Transport{
				TLSClientConfig: tlsCfg,
				Proxy:           http.ProxyFromEnvironment,
			},
		},
	}, nil
}

// Namespace returns the namespace this client targets.
func (c *Client) Namespace() string { return c.namespace }

// endpoint builds an absolute URL for the given path segments under the base.
// Segments are kept in URL.Path (decoded form) so URL.String() percent-encodes
// reserved characters — important for routine names like "%ZVISTA.mac".
func (c *Client) endpoint(segments ...string) *url.URL {
	u := *c.base // copy
	parts := []string{strings.TrimSuffix(u.Path, "/")}
	parts = append(parts, segments...)
	u.Path = strings.Join(parts, "/")
	u.RawPath = "" // force re-derivation from Path on String()
	return &u
}

// get issues a GET and decodes the Atelier envelope into out, mapping HTTP and
// server-side (status.errors) failures to a Go error.
func (c *Client) get(ctx context.Context, u *url.URL, out *Envelope) error {
	return c.do(ctx, http.MethodGet, u, nil, out)
}

// do issues an HTTP request with an optional JSON body and decodes the Atelier
// envelope into out, mapping HTTP and server-side (status.errors) failures to a
// Go error. It is the single request path for both the read (GET) and write
// (PUT/POST) sides, so auth, the response cap, and error mapping are identical.
func (c *Client) do(ctx context.Context, method string, u *url.URL, body []byte, out *Envelope) error {
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), rdr)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	// App auth: a bearer token wins over basic auth; either rides on top of the
	// (optional) mTLS transport.
	switch {
	case c.token != "":
		req.Header.Set("Authorization", "Bearer "+c.token)
	case c.user != "":
		req.SetBasicAuth(c.user, c.password)
	}

	resp, err := c.hc.Do(req)
	if err != nil {
		return fmt.Errorf("atelier: %s %s: %w", method, u.Path, err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse))
	if err != nil {
		return fmt.Errorf("atelier: read response from %s: %w", u.Path, err)
	}
	// Auth failures carry no useful envelope; surface them as typed HTTPErrors so
	// doctor/lifecycle can tell 401 (bad credential) from 403 (no privilege).
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return &HTTPError{Status: resp.StatusCode, Method: method, Path: u.Path}
	}

	// Atelier returns its envelope (with status.errors) even on 4xx/5xx, so try
	// to decode before falling back to a typed HTTP-status error.
	if len(data) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			if resp.StatusCode >= 400 {
				return &HTTPError{Status: resp.StatusCode, Method: method, Path: u.Path}
			}
			return fmt.Errorf("atelier: decode response from %s: %w", u.Path, err)
		}
	} else if resp.StatusCode >= 400 {
		return &HTTPError{Status: resp.StatusCode, Method: method, Path: u.Path}
	}
	if err := out.Status.firstError(); err != nil {
		return fmt.Errorf("atelier: %s: %w", u.Path, err)
	}
	if resp.StatusCode >= 400 {
		return &HTTPError{Status: resp.StatusCode, Method: method, Path: u.Path}
	}
	return nil
}

// firstError returns the first non-empty server error, or nil.
func (s Status) firstError() error {
	for _, e := range s.Errors {
		if e.Error == "" {
			continue
		}
		if e.Code != "" {
			return fmt.Errorf("%s (%s)", e.Error, e.Code)
		}
		return errors.New(e.Error)
	}
	return nil
}
