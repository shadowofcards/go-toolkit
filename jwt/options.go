package jwt

import (
	"encoding/json"
	"net/http"
	"time"
)

// Option configures the Verifier.
type Option func(*config) error

type config struct {
	issuer            string
	jwksURL           string
	jwksJSON          json.RawMessage
	skipTLSVerify     bool
	httpClient        *http.Client
	refreshInterval   time.Duration
	refreshUnknownKID bool
	errHandler        func(error)
}

// WithIssuer sets the issuer base URL (used to derive the default JWKS URL).
func WithIssuer(iss string) Option {
	return func(c *config) error {
		c.issuer = iss
		return nil
	}
}

// WithJWKSURL sets the JWKS endpoint explicitly.
func WithJWKSURL(url string) Option {
	return func(c *config) error {
		c.jwksURL = url
		return nil
	}
}

// WithJWKSetJSON provides a JWK Set in JSON form, bypassing any HTTP fetch.
func WithJWKSetJSON(raw json.RawMessage) Option {
	return func(c *config) error {
		c.jwksJSON = raw
		return nil
	}
}

// WithSkipTLSVerify controls whether TLS verification is skipped when fetching JWKS over HTTPS.
func WithSkipTLSVerify(skip bool) Option {
	return func(c *config) error {
		c.skipTLSVerify = skip
		return nil
	}
}

// WithHTTPClient provides a custom HTTP client for JWKS fetching.
func WithHTTPClient(client *http.Client) Option {
	return func(c *config) error {
		c.httpClient = client
		return nil
	}
}

// WithRefreshInterval sets how often the JWKS should be refreshed when fetched over HTTP.
func WithRefreshInterval(d time.Duration) Option {
	return func(c *config) error {
		c.refreshInterval = d
		return nil
	}
}

// WithRefreshUnknownKID controls whether the JWKS refresher will attempt to fetch unknown KIDs.
func WithRefreshUnknownKID(allow bool) Option {
	return func(c *config) error {
		c.refreshUnknownKID = allow
		return nil
	}
}

// WithErrorHandler registers a callback to receive JWKS refresh errors.
func WithErrorHandler(fn func(error)) Option {
	return func(c *config) error {
		c.errHandler = fn
		return nil
	}
}
