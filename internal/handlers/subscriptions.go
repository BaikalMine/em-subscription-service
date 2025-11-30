package handlers

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/BaikalMine/em-subscription-service/internal/storage"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
)

// Handler связывает эндпоинты подписок со стором и логгером.
type Handler struct {
	store  *storage.Store
	logger *logrus.Logger
}

// NewHandler создаёт обработчик с настроенным стором и логгером.
func NewHandler(store *storage.Store, logger *logrus.Logger) *Handler {
	return &Handler{store: store, logger: logger}
}

// RegisterRoutes регистрирует маршруты подписок на роутере.
func (h *Handler) RegisterRoutes(r chi.Router) {
	r.Route("/subscriptions", func(r chi.Router) {
		r.Get("/summary", h.summary)
		r.Get("/", h.listSubscriptions)
		r.Post("/", h.createSubscription)
		r.Get("/{id}", h.getSubscription)
		r.Put("/{id}", h.updateSubscription)
		r.Delete("/{id}", h.deleteSubscription)
	})
}

func (h *Handler) createSubscription(w http.ResponseWriter, r *http.Request) {
	var req subscriptionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.logRequest(r, http.StatusBadRequest, err)
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid body"})
		return
	}
	sub, err := req.toStorage()
	if err != nil {
		h.logRequest(r, http.StatusBadRequest, err)
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
		return
	}

	if err := h.store.Create(r.Context(), sub); err != nil {
		h.logRequest(r, http.StatusInternalServerError, err)
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "unable to persist subscription"})
		return
	}

	writeJSON(w, http.StatusCreated, convertResponse(sub))
}

func (h *Handler) listSubscriptions(w http.ResponseWriter, r *http.Request) {
	filter, err := buildListFilter(r)
	if err != nil {
		h.logRequest(r, http.StatusBadRequest, err)
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
		return
	}
	result, err := h.store.List(r.Context(), filter)
	if err != nil {
		h.logRequest(r, http.StatusInternalServerError, err)
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "unable to fetch subscriptions"})
		return
	}

	resp := make([]subscriptionResponse, 0, len(result))
	for _, sub := range result {
		resp = append(resp, convertResponse(&sub))
	}

	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) getSubscription(w http.ResponseWriter, r *http.Request) {
	subID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		h.logRequest(r, http.StatusBadRequest, err)
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid id"})
		return
	}

	sub, err := h.store.Get(r.Context(), subID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, errorResponse{Error: "subscription not found"})
			return
		}
		h.logRequest(r, http.StatusInternalServerError, err)
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to load subscription"})
		return
	}

	writeJSON(w, http.StatusOK, convertResponse(sub))
}

func (h *Handler) updateSubscription(w http.ResponseWriter, r *http.Request) {
	subID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		h.logRequest(r, http.StatusBadRequest, err)
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid id"})
		return
	}

	var req subscriptionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.logRequest(r, http.StatusBadRequest, err)
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid body"})
		return
	}

	sub, err := req.toStorage()
	if err != nil {
		h.logRequest(r, http.StatusBadRequest, err)
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
		return
	}

	sub.ID = subID
	if err := h.store.Update(r.Context(), sub); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, errorResponse{Error: "subscription not found"})
			return
		}
		h.logRequest(r, http.StatusInternalServerError, err)
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "unable to update subscription"})
		return
	}

	writeJSON(w, http.StatusOK, convertResponse(sub))
}

func (h *Handler) deleteSubscription(w http.ResponseWriter, r *http.Request) {
	subID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		h.logRequest(r, http.StatusBadRequest, err)
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid id"})
		return
	}

	if err := h.store.Delete(r.Context(), subID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, errorResponse{Error: "subscription not found"})
			return
		}
		h.logRequest(r, http.StatusInternalServerError, err)
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "unable to remove subscription"})
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) summary(w http.ResponseWriter, r *http.Request) {
	start := strings.TrimSpace(r.URL.Query().Get("start"))
	end := strings.TrimSpace(r.URL.Query().Get("end"))
	if start == "" || end == "" {
		h.logRequest(r, http.StatusBadRequest, errors.New("missing start/end"))
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "start and end query parameters are required (format MM-YYYY)"})
		return
	}

	periodStart, err := parseMonthYear(start)
	if err != nil {
		h.logRequest(r, http.StatusBadRequest, err)
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid start format"})
		return
	}
	periodEnd, err := parseMonthYear(end)
	if err != nil {
		h.logRequest(r, http.StatusBadRequest, err)
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid end format"})
		return
	}
	if periodEnd.Before(periodStart) {
		h.logRequest(r, http.StatusBadRequest, errors.New("end before start"))
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "end must not be before start"})
		return
	}

	filter := storage.SummaryFilter{
		PeriodStart: startOfMonth(periodStart),
		PeriodEnd:   endOfMonth(periodEnd),
	}

	if user := strings.TrimSpace(r.URL.Query().Get("user_id")); user != "" {
		uid, err := uuid.Parse(user)
		if err != nil {
			h.logRequest(r, http.StatusBadRequest, err)
			writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid user_id"})
			return
		}
		filter.UserID = &uid
	}

	if service := strings.TrimSpace(r.URL.Query().Get("service_name")); service != "" {
		filter.ServiceName = &service
	}

	total, err := h.store.Summary(r.Context(), filter)
	if err != nil {
		h.logRequest(r, http.StatusInternalServerError, err)
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "unable to calculate total"})
		return
	}

	writeJSON(w, http.StatusOK, summaryResponse{TotalPrice: total})
}

// buildListFilter формирует фильтры из query параметров для списка.
func buildListFilter(r *http.Request) (storage.ListFilter, error) {
	var filter storage.ListFilter
	query := r.URL.Query()
	if user := strings.TrimSpace(query.Get("user_id")); user != "" {
		uid, err := uuid.Parse(user)
		if err != nil {
			return filter, fmt.Errorf("invalid user_id")
		}
		filter.UserID = &uid
	}
	if service := strings.TrimSpace(query.Get("service_name")); service != "" {
		filter.ServiceName = &service
	}
	if limit := strings.TrimSpace(query.Get("limit")); limit != "" {
		val, err := strconv.Atoi(limit)
		if err != nil {
			return filter, fmt.Errorf("limit must be an integer")
		}
		filter.Limit = val
	}
	if offset := strings.TrimSpace(query.Get("offset")); offset != "" {
		val, err := strconv.Atoi(offset)
		if err != nil {
			return filter, fmt.Errorf("offset must be an integer")
		}
		filter.Offset = val
	}
	return filter, nil
}

// toStorage переводит DTO запроса в модель хранилища.
func (req *subscriptionRequest) toStorage() (*storage.Subscription, error) {
	if strings.TrimSpace(req.ServiceName) == "" {
		return nil, errors.New("service_name is required")
	}
	if req.Price < 0 {
		return nil, errors.New("price must be non-negative")
	}
	userID, err := uuid.Parse(req.UserID)
	if err != nil {
		return nil, errors.New("invalid user_id")
	}
	if strings.TrimSpace(req.StartDate) == "" {
		return nil, errors.New("start_date is required")
	}
	start, err := parseMonthYear(req.StartDate)
	if err != nil {
		return nil, err
	}

	var endPtr *time.Time
	if req.EndDate != nil {
		end, err := parseMonthYear(*req.EndDate)
		if err != nil {
			return nil, err
		}
		parsed := startOfMonth(end)
		endPtr = &parsed
	}

	return &storage.Subscription{
		ServiceName: req.ServiceName,
		Price:       req.Price,
		UserID:      userID,
		StartDate:   startOfMonth(start),
		EndDate:     endPtr,
	}, nil
}

// convertResponse собирает ответ API из модели подписки.
func convertResponse(sub *storage.Subscription) subscriptionResponse {
	resp := subscriptionResponse{
		ID:          sub.ID.String(),
		ServiceName: sub.ServiceName,
		Price:       sub.Price,
		UserID:      sub.UserID.String(),
		StartDate:   formatMonthYear(sub.StartDate),
		CreatedAt:   sub.CreatedAt,
	}
	if sub.EndDate != nil {
		end := formatMonthYear(*sub.EndDate)
		resp.EndDate = &end
	}
	return resp
}

// parseMonthYear разбирает строку MM-YYYY в time.Time.
func parseMonthYear(value string) (time.Time, error) {
	parsed, err := time.Parse("01-2006", value)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid month-year format %q", value)
	}
	return parsed, nil
}

// startOfMonth возвращает первый день месяца в UTC.
func startOfMonth(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
}

// endOfMonth возвращает последний момент месяца.
func endOfMonth(t time.Time) time.Time {
	first := startOfMonth(t)
	return first.AddDate(0, 1, -1)
}

// formatMonthYear форматирует дату как MM-YYYY.
func formatMonthYear(t time.Time) string {
	return t.Format("01-2006")
}

// writeJSON устанавливает заголовки и сериализует ответ.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// logRequest пишет структуру запроса в лог.
func (h *Handler) logRequest(r *http.Request, status int, err error) {
	h.logger.WithFields(logrus.Fields{
		"method": r.Method,
		"path":   r.URL.Path,
		"status": status,
		"error":  err,
	}).Info("handled request")
}

type subscriptionRequest struct {
	ServiceName string  `json:"service_name"`
	Price       int     `json:"price"`
	UserID      string  `json:"user_id"`
	StartDate   string  `json:"start_date"`
	EndDate     *string `json:"end_date"`
}

type subscriptionResponse struct {
	ID          string    `json:"id"`
	ServiceName string    `json:"service_name"`
	Price       int       `json:"price"`
	UserID      string    `json:"user_id"`
	StartDate   string    `json:"start_date"`
	EndDate     *string   `json:"end_date,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

type summaryResponse struct {
	TotalPrice int `json:"total_price"`
}

type errorResponse struct {
	Error string `json:"error"`
}
