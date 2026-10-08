package handler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/MohsenDowlati/shorts/internal/auth"
	"github.com/MohsenDowlati/shorts/internal/domain"
	"github.com/MohsenDowlati/shorts/internal/service"
	"github.com/go-chi/chi/v5"
)

const (
	analyticsDateLayout       = "2006-01-02"
	defaultAnalyticsRangeDays = 30
	defaultMaxQueryDays       = 366
)

type LinkAnalyticsProvider interface {
	Get(context.Context, string, string, string, string) (*domain.LinkAnalytics, error)
}

type AnalyticsHandler struct {
	service LinkAnalyticsProvider
	logger  *slog.Logger
	now     func() time.Time
	maxDays int
}

func NewAnalyticsHandler(analyticsService LinkAnalyticsProvider, logger *slog.Logger) *AnalyticsHandler {
	return NewAnalyticsHandlerWithMaxQueryDays(analyticsService, logger, defaultMaxQueryDays)
}

func NewAnalyticsHandlerWithMaxQueryDays(analyticsService LinkAnalyticsProvider, logger *slog.Logger, maxQueryDays int) *AnalyticsHandler {
	if maxQueryDays <= 0 {
		maxQueryDays = defaultMaxQueryDays
	}
	return &AnalyticsHandler{
		service: analyticsService,
		logger:  logger.With(slog.String("component", "analytics_handler")),
		now:     time.Now,
		maxDays: maxQueryDays,
	}
}

func (handler *AnalyticsHandler) LinkAnalytics(w http.ResponseWriter, r *http.Request) {
	userID := auth.UserID(r.Context())
	if userID == "" {
		WriteError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	from, to, err := parseAnalyticsDateRangeWithMaxDays(r, handler.now().UTC(), handler.maxDays)
	if err != nil {
		WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	analytics, err := handler.service.Get(
		r.Context(),
		strings.TrimSpace(chi.URLParam(r, "code")),
		userID,
		from,
		to,
	)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrLinkNotFound):
			WriteError(w, http.StatusNotFound, "link not found")
		case errors.Is(err, service.ErrAnalyticsForbidden):
			WriteError(w, http.StatusForbidden, "forbidden")
		default:
			handler.logger.ErrorContext(r.Context(), "failed to get link analytics", slog.Any("error", err))
			WriteError(w, http.StatusInternalServerError, "failed to get link analytics")
		}
		return
	}

	WriteJSON(w, http.StatusOK, analytics)
}

func parseAnalyticsDateRange(r *http.Request, now time.Time) (string, string, error) {
	return parseAnalyticsDateRangeWithMaxDays(r, now, defaultMaxQueryDays)
}

func parseAnalyticsDateRangeWithMaxDays(r *http.Request, now time.Time, maxQueryDays int) (string, string, error) {
	today, err := time.Parse(analyticsDateLayout, now.UTC().Format(analyticsDateLayout))
	if err != nil {
		return "", "", fmt.Errorf("calculate current UTC date: %w", err)
	}

	fromValue := strings.TrimSpace(r.URL.Query().Get("from"))
	toValue := strings.TrimSpace(r.URL.Query().Get("to"))

	var fromDate time.Time
	var toDate time.Time
	switch {
	case fromValue == "" && toValue == "":
		toDate = today
		fromDate = today.AddDate(0, 0, -(defaultAnalyticsRangeDays - 1))
	case fromValue == "":
		toDate, err = parseAnalyticsDate("to", toValue)
		if err != nil {
			return "", "", err
		}
		fromDate = toDate.AddDate(0, 0, -(defaultAnalyticsRangeDays - 1))
	case toValue == "":
		fromDate, err = parseAnalyticsDate("from", fromValue)
		if err != nil {
			return "", "", err
		}
		toDate = today
	default:
		fromDate, err = parseAnalyticsDate("from", fromValue)
		if err != nil {
			return "", "", err
		}
		toDate, err = parseAnalyticsDate("to", toValue)
		if err != nil {
			return "", "", err
		}
	}

	if fromDate.After(toDate) {
		return "", "", errors.New("from must be on or before to")
	}
	days := int(toDate.Sub(fromDate).Hours()/24) + 1
	if days > maxQueryDays {
		return "", "", fmt.Errorf("date range must not exceed %d days", maxQueryDays)
	}

	return fromDate.Format(analyticsDateLayout), toDate.Format(analyticsDateLayout), nil
}

func parseAnalyticsDate(name, value string) (time.Time, error) {
	date, err := time.Parse(analyticsDateLayout, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("%s must use YYYY-MM-DD format", name)
	}
	return date, nil
}
