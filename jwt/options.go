package jwt

import (
	"net/http"
	"time"
)

type Option func(*cfg)

func WithIssuer(iss string) Option          { return func(c *cfg) { c.issuer = iss } }
func WithJWKSURL(url string) Option         { return func(c *cfg) { c.jwksURL = url } }
func WithSkipTLSVerify(b bool) Option       { return func(c *cfg) { c.skipTLSVerify = b } }
func WithHTTPClient(cl *http.Client) Option { return func(c *cfg) { c.httpClient = cl } }
func WithRefreshInterval(d time.Duration) Option {
	return func(c *cfg) { c.refreshInterval = d }
}
func WithRefreshUnknownKID(b bool) Option { return func(c *cfg) { c.refreshUnknownKID = b } }
func WithErrorHandler(fn func(error)) Option {
	return func(c *cfg) { c.errHandler = fn }
}
