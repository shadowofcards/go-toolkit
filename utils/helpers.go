package utils

import (
	"net/http"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	apperr "github.com/leandrodaf/go-toolkit/errors"
)

type PaginationMeta struct {
	Total int64 `json:"total"`
	Page  int64 `json:"page"`
	Limit int64 `json:"limit"`
}

type Pagination struct {
	DefaultPage  int64
	DefaultLimit int64
	MaxLimit     int64
}

type paginateQuery struct {
	Page  int64 `query:"page,default:1"`
	Limit int64 `query:"limit,default:10"`
}

func NewPagination() Pagination {
	return Pagination{DefaultPage: 1, DefaultLimit: 10, MaxLimit: 100}
}

func (p Pagination) Parse(c fiber.Ctx) (page, limit, offset int64) {
	var q paginateQuery
	_ = c.Bind().Query(&q)
	if q.Page < 1 {
		q.Page = p.DefaultPage
	}
	if q.Limit < 1 {
		q.Limit = p.DefaultLimit
	}
	if q.Limit > p.MaxLimit {
		q.Limit = p.MaxLimit
	}
	page = q.Page
	limit = q.Limit
	offset = (page - 1) * limit
	return
}

func (p Pagination) Meta(total int64, page, limit int64) *PaginationMeta {
	return &PaginationMeta{Total: total, Page: page, Limit: limit}
}

func GetPathID(c fiber.Ctx) (uuid.UUID, error) {
	idStr := c.Params("id")
	if idStr == "" {
		return uuid.Nil, apperr.New().
			WithHTTPStatus(http.StatusBadRequest).
			WithCode("MISSING_ID").
			WithMessage("missing :id path param")
	}
	id, err := uuid.Parse(idStr)
	if err != nil {
		return uuid.Nil, apperr.New().
			WithHTTPStatus(http.StatusBadRequest).
			WithCode("INVALID_ID").
			WithMessage("invalid uuid")
	}
	return id, nil
}

func GetBody(c fiber.Ctx, v any) error {
	if err := c.Bind().JSON(v); err != nil {
		return apperr.New().
			WithHTTPStatus(http.StatusBadRequest).
			WithCode("INVALID_BODY").
			WithMessage("invalid json body").
			WithError(err)
	}
	return nil
}

func BindQuery(c fiber.Ctx, v any) error {
	if err := c.Bind().Query(v); err != nil {
		return apperr.New().
			WithHTTPStatus(http.StatusBadRequest).
			WithCode("INVALID_QUERY").
			WithMessage("invalid query params").
			WithError(err)
	}
	return nil
}

func MustGetBody(c fiber.Ctx, v any) { _ = GetBody(c, v) }

func MustBindQuery(c fiber.Ctx, v any) { _ = BindQuery(c, v) }

func GetPlayerID(c fiber.Ctx) (uuid.UUID, error) {
	raw := c.Locals("playerID")
	if raw == nil {
		return uuid.Nil, apperr.New().
			WithHTTPStatus(http.StatusUnauthorized).
			WithCode("PLAYER_ID_MISSING").
			WithMessage("playerID not found")
	}
	str, ok := raw.(string)
	if !ok || str == "" {
		return uuid.Nil, apperr.New().
			WithHTTPStatus(http.StatusUnauthorized).
			WithCode("PLAYER_ID_INVALID").
			WithMessage("invalid playerID")
	}
	id, err := uuid.Parse(str)
	if err != nil {
		return uuid.Nil, apperr.New().
			WithHTTPStatus(http.StatusUnauthorized).
			WithCode("PLAYER_ID_INVALID").
			WithMessage("invalid playerID")
	}
	return id, nil
}

func ParseOptionalUUID(s string) (*uuid.UUID, error) {
	if s == "" {
		return nil, nil
	}
	u, err := uuid.Parse(s)
	if err != nil {
		return nil, apperr.New().
			WithHTTPStatus(http.StatusBadRequest).
			WithCode("INVALID_UUID").
			WithMessage("invalid uuid").
			WithError(err)
	}
	return &u, nil
}
