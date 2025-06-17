package jwt

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/MicahParks/keyfunc/v3"
	gjwt "github.com/golang-jwt/jwt/v5"

	apperrors "github.com/shadowofcards/go-toolkit/errors"
)

var (
	ErrConfig = apperrors.New().
			WithHTTPStatus(http.StatusInternalServerError).
			WithCode("JWT_CONFIG_ERROR").
			WithMessage("JWT configuration is invalid")

	ErrJWKSParse = apperrors.New().
			WithHTTPStatus(http.StatusInternalServerError).
			WithCode("JWKS_PARSE_ERROR").
			WithMessage("failed to parse JWK Set")

	ErrInvalidToken = apperrors.New().
			WithHTTPStatus(http.StatusUnauthorized).
			WithCode("INVALID_JWT").
			WithMessage("invalid or expired token")
)

type Verifier struct {
	kf keyfunc.Keyfunc
}

func New(opts ...Option) (*Verifier, error) {
	cfg := &config{refreshInterval: time.Hour}
	for _, o := range opts {
		if err := o(cfg); err != nil {
			return nil, ErrConfig.WithError(err)
		}
	}

	var kf keyfunc.Keyfunc
	var err error

	if len(cfg.jwksJSON) > 0 {

		kf, err = keyfunc.NewJWKSetJSON(cfg.jwksJSON)
		if err != nil {
			return nil, ErrJWKSParse.WithError(err)
		}
	} else {

		if cfg.jwksURL == "" && cfg.issuer == "" {
			return nil, ErrConfig
		}
		if cfg.jwksURL == "" {
			iss := strings.TrimRight(cfg.issuer, "/")
			cfg.jwksURL = iss + "/protocol/openid-connect/certs"
		}

		kf, err = keyfunc.NewDefaultOverrideCtx(
			context.Background(),
			[]string{cfg.jwksURL},
			keyfunc.Override{
				RefreshInterval: cfg.refreshInterval,
			},
		)
		if err != nil {
			return nil, ErrJWKSParse.WithError(err)
		}
	}

	return &Verifier{kf: kf}, nil
}

func (v *Verifier) Validate(
	ctx context.Context,
	tokenString string,
	claims gjwt.Claims,
	allowExpired bool,
) error {
	token, err := gjwt.ParseWithClaims(
		tokenString,
		claims,
		v.kf.KeyfuncCtx(ctx),
	)
	if err != nil {
		if allowExpired && errors.Is(err, gjwt.ErrTokenExpired) {
			return nil
		}
		return ErrInvalidToken.WithError(err)
	}
	if !token.Valid {
		return ErrInvalidToken
	}
	return nil
}
