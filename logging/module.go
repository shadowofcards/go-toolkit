package logging

import "go.uber.org/fx"

type Params struct {
	fx.In
	Options []Option `group:"logger_options"`
}

func provideLogger(p Params) (*Logger, error) {
	return New(p.Options...)
}

func Module() fx.Option {
	return fx.Options(
		fx.Provide(provideLogger),
	)
}
