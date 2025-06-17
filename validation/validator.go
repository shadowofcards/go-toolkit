package validation

import (
	"context"

	"github.com/go-playground/validator/v10"
)

type Validator interface {
	Struct(any) error
	StructCtx(context.Context, any) error
	Var(any, string) error
	VarCtx(context.Context, any, string) error
}

type Option func(*validator.Validate) error

func WithRule(tag string, fn validator.Func) Option {
	return func(v *validator.Validate) error {
		return v.RegisterValidation(tag, fn)
	}
}

func New(opts ...Option) (Validator, error) {
	v := validator.New()
	for _, opt := range opts {
		if err := opt(v); err != nil {
			return nil, err
		}
	}
	return v, nil
}
