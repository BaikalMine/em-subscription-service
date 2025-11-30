package storage

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Store управляет сохранением записей подписок.
type Store struct {
	db *sql.DB
}

// Subscription описывает одну запись о подписке.
type Subscription struct {
	ID          uuid.UUID  `json:"id"`
	ServiceName string     `json:"service_name"`
	Price       int        `json:"price"`
	UserID      uuid.UUID  `json:"user_id"`
	StartDate   time.Time  `json:"start_date"`
	EndDate     *time.Time `json:"end_date,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
}

// ListFilter задаёт опциональные фильтры для списка подписок.
type ListFilter struct {
	UserID      *uuid.UUID
	ServiceName *string
	Limit       int
	Offset      int
}

// SummaryFilter описывает параметры подсчёта суммарной стоимости.
type SummaryFilter struct {
	PeriodStart time.Time
	PeriodEnd   time.Time
	UserID      *uuid.UUID
	ServiceName *string
}

// NewStore создаёт объект Store на основе переданного sql.DB.
func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// Create сохраняет запись подписки и заполняет id и created_at.
func (s *Store) Create(ctx context.Context, sub *Subscription) error {
	if sub.ID == uuid.Nil {
		sub.ID = uuid.New()
	}

	var createdAt time.Time
	err := s.db.QueryRowContext(ctx,
		`INSERT INTO subscriptions (id, service_name, price, user_id, start_date, end_date)
VALUES ($1, $2, $3, $4, $5, $6) RETURNING created_at`,
		sub.ID, sub.ServiceName, sub.Price, sub.UserID, sub.StartDate, sub.EndDate,
	).Scan(&createdAt)
	if err != nil {
		return err
	}

	sub.CreatedAt = createdAt
	return nil
}

// Get загружает подписку по id.
func (s *Store) Get(ctx context.Context, id uuid.UUID) (*Subscription, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, service_name, price, user_id, start_date, end_date, created_at
FROM subscriptions WHERE id = $1`, id)
	sub, err := scanSubscription(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, err
		}
		return nil, err
	}
	return sub, nil
}

// List возвращает подписки, подходящие под фильтры.
func (s *Store) List(ctx context.Context, filter ListFilter) ([]Subscription, error) {
	query := `SELECT id, service_name, price, user_id, start_date, end_date, created_at FROM subscriptions`
	args := make([]any, 0, 4)
	clauses := make([]string, 0, 2)

	if filter.UserID != nil {
		args = append(args, *filter.UserID)
		clauses = append(clauses, fmt.Sprintf("user_id = $%d", len(args)))
	}
	if filter.ServiceName != nil {
		args = append(args, *filter.ServiceName)
		clauses = append(clauses, fmt.Sprintf("service_name ILIKE $%d", len(args)))
	}

	if len(clauses) > 0 {
		query += " WHERE " + strings.Join(clauses, " AND ")
	}

	query += " ORDER BY created_at DESC"

	if filter.Limit > 0 {
		args = append(args, filter.Limit)
		query += fmt.Sprintf(" LIMIT $%d", len(args))
	}
	if filter.Offset > 0 {
		args = append(args, filter.Offset)
		query += fmt.Sprintf(" OFFSET $%d", len(args))
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make([]Subscription, 0)
	for rows.Next() {
		sub, err := scanSubscription(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, *sub)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return result, nil
}

// Update обновляет существующую запись подписки.
func (s *Store) Update(ctx context.Context, sub *Subscription) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE subscriptions SET service_name = $1, price = $2, user_id = $3, start_date = $4, end_date = $5 WHERE id = $6`,
		sub.ServiceName, sub.Price, sub.UserID, sub.StartDate, sub.EndDate, sub.ID,
	)
	if err != nil {
		return err
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return sql.ErrNoRows
	}

	return nil
}

// Delete удаляет запись подписки по идентификатору.
func (s *Store) Delete(ctx context.Context, id uuid.UUID) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM subscriptions WHERE id = $1`, id)
	if err != nil {
		return err
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return sql.ErrNoRows
	}

	return nil
}

// Summary считает суммарную цену подписок, пересекающихся с периодом.
func (s *Store) Summary(ctx context.Context, filter SummaryFilter) (int, error) {
	args := []any{filter.PeriodEnd, filter.PeriodStart}
	query := `SELECT COALESCE(SUM(price), 0) FROM subscriptions WHERE start_date <= $1 AND (end_date IS NULL OR end_date >= $2)`

	if filter.UserID != nil {
		args = append(args, *filter.UserID)
		query += fmt.Sprintf(" AND user_id = $%d", len(args))
	}
	if filter.ServiceName != nil {
		args = append(args, *filter.ServiceName)
		query += fmt.Sprintf(" AND service_name ILIKE $%d", len(args))
	}

	var total int
	if err := s.db.QueryRowContext(ctx, query, args...).Scan(&total); err != nil {
		return 0, err
	}

	return total, nil
}

// scanSubscription собирает модель из результата запроса.
func scanSubscription(scanner interface {
	Scan(dest ...any) error
}) (*Subscription, error) {
	var sub Subscription
	var endDate sql.NullTime
	if err := scanner.Scan(
		&sub.ID, &sub.ServiceName, &sub.Price, &sub.UserID, &sub.StartDate, &endDate, &sub.CreatedAt,
	); err != nil {
		return nil, err
	}
	if endDate.Valid {
		sub.EndDate = &endDate.Time
	}
	return &sub, nil
}
