package jwt

import (
	"crypto/tls"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/MicahParks/keyfunc"
	gjwt "github.com/golang-jwt/jwt/v4"
)

type Verifier struct{ jwks *keyfunc.JWKS }

type cfg struct {
	issuer            string
	jwksURL           string
	skipTLSVerify     bool
	httpClient        *http.Client
	refreshInterval   time.Duration
	refreshUnknownKID bool
	errHandler        func(error)
}

func New(opts ...Option) (*Verifier, error) {
	c := &cfg{
		refreshInterval:   time.Hour,
		refreshUnknownKID: true,
		errHandler:        func(error) {},
	}

	for _, opt := range opts {
		opt(c)
	}

	if c.jwksURL == "" && c.issuer == "" {
		return nil, errors.New("jwt: either issuer or jwks URL must be set")
	}
	if c.jwksURL == "" {
		iss := strings.TrimRight(c.issuer, "/")
		c.jwksURL = iss + "/protocol/openid-connect/certs"
	}

	if c.httpClient == nil {
		if c.skipTLSVerify {
			c.httpClient = &http.Client{
				Timeout: 10 * time.Second,
				Transport: &http.Transport{
					TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
				},
			}
		} else {
			c.httpClient = http.DefaultClient
		}
	}

	jwks, err := keyfunc.Get(c.jwksURL, keyfunc.Options{
		Client:            c.httpClient,
		RefreshInterval:   c.refreshInterval,
		RefreshUnknownKID: c.refreshUnknownKID,
		RefreshErrorHandler: func(err error) {
			c.errHandler(err)
		},
	})
	if err != nil {
		return nil, err
	}

	return &Verifier{jwks: jwks}, nil
}

func (v *Verifier) Shutdown() { v.jwks.EndBackground() }

func (v *Verifier) Validate(tokenString string, claims gjwt.Claims, allowExpired bool) error {
	parsed, err := gjwt.ParseWithClaims(tokenString, claims, v.jwks.Keyfunc)
	if err != nil {
		if errors.Is(err, gjwt.ErrTokenExpired) && allowExpired {
			return nil
		}
		return err
	}
	if !parsed.Valid && !(allowExpired && isOnlyExpiredErr(parsed)) {
		return errors.New("invalid token")
	}
	return nil
}

func isOnlyExpiredErr(t *gjwt.Token) bool { return t != nil && !t.Valid }
