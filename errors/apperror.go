package errors

import (
	"errors"
	"fmt"
	"net/http"
)

type AppError struct {
	Err        error
	HTTPStatus int
	Code       string
	Message    string
	Context    map[string]interface{}
}

func New() *AppError {
	return &AppError{
		HTTPStatus: http.StatusInternalServerError,
		Context:    map[string]interface{}{},
	}
}

func (e *AppError) clone() *AppError {
	c := *e
	c.Context = make(map[string]interface{}, len(e.Context))
	for k, v := range e.Context {
		c.Context[k] = v
	}
	return &c
}

func (e *AppError) WithError(err error) *AppError {
	c := e.clone()
	c.Err = err
	return c
}

func (e *AppError) WithHTTPStatus(status int) *AppError {
	c := e.clone()
	c.HTTPStatus = status
	return c
}

func (e *AppError) WithCode(code string) *AppError {
	c := e.clone()
	c.Code = code
	return c
}

func (e *AppError) WithMessage(msg string) *AppError {
	c := e.clone()
	c.Message = msg
	return c
}

func (e *AppError) WithContext(key string, value interface{}) *AppError {
	c := e.clone()
	c.Context[key] = value
	return c
}

func (e *AppError) Error() string {
	if e.Err != nil && e.Message != "" {
		return fmt.Sprintf("%s: %v", e.Message, e.Err)
	}
	if e.Err != nil {
		return e.Err.Error()
	}
	if e.Message != "" {
		return e.Message
	}
	return fmt.Sprintf("code=%s status=%d", e.Code, e.HTTPStatus)
}

func (e *AppError) Unwrap() error {
	return e.Err
}

func (e *AppError) Status() int {
	return e.HTTPStatus
}

func (e *AppError) ErrCode() string {
	return e.Code
}

func FromError(err error) (*AppError, bool) {
	var ae *AppError
	if errors.As(err, &ae) {
		return ae, true
	}
	return nil, false
}
