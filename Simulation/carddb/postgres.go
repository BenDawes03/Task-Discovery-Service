package carddb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "github.com/lib/pq"
)

type Record struct {
	CardID       string
	Active       bool
	Blocked      bool
	BalanceCents int64
}

type Store struct {
	db *sql.DB
}

func Open(dsn string) (*Store, error) {
	if dsn == "" {
		return nil, fmt.Errorf("empty dsn")
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, fmt.Errorf("open postgres: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}

	// modest pool defaults for simulation workloads
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(2)
	db.SetConnMaxLifetime(5 * time.Minute)

	return &Store{db: db}, nil
}

func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *Store) EnsureSchema(ctx context.Context) error {
	q := `
		CREATE TABLE IF NOT EXISTS cards (
			card_id TEXT PRIMARY KEY,
			active BOOLEAN NOT NULL,
			blocked BOOLEAN NOT NULL,
			balance_cents BIGINT NOT NULL DEFAULT 0,
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);
		ALTER TABLE cards ADD COLUMN IF NOT EXISTS balance_cents BIGINT NOT NULL DEFAULT 0;
	`
	_, err := s.db.ExecContext(ctx, q)
	return err
}

func (s *Store) Upsert(ctx context.Context, r Record) error {
	q := `
		INSERT INTO cards (card_id, active, blocked, balance_cents, updated_at)
		VALUES ($1, $2, $3, $4, NOW())
		ON CONFLICT (card_id) DO UPDATE
		SET active = EXCLUDED.active,
			blocked = EXCLUDED.blocked,
			balance_cents = EXCLUDED.balance_cents,
			updated_at = NOW();
	`
	_, err := s.db.ExecContext(ctx, q, r.CardID, r.Active, r.Blocked, r.BalanceCents)
	return err
}

func (s *Store) Seed(ctx context.Context, records []Record) error {
	if len(records) == 0 {
		return nil
	}

	// For large seeds, use bulk INSERTs to avoid per-row round trips.
	if len(records) > 100 {
		return s.seedCardsBulk(ctx, records)
	}

	// Non-destructive seed: only inserts if missing.
	q := `
		INSERT INTO cards (card_id, active, blocked, balance_cents, updated_at)
		VALUES ($1, $2, $3, $4, NOW())
		ON CONFLICT (card_id) DO NOTHING;
	`
	for _, r := range records {
		if r.CardID == "" {
			continue
		}
		if _, err := s.db.ExecContext(ctx, q, r.CardID, r.Active, r.Blocked, r.BalanceCents); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) seedCardsBulk(ctx context.Context, records []Record) error {
	const chunkSize = 1000

	filtered := make([]Record, 0, len(records))
	for _, r := range records {
		if strings.TrimSpace(r.CardID) == "" {
			continue
		}
		filtered = append(filtered, r)
	}
	if len(filtered) == 0 {
		return nil
	}

	for start := 0; start < len(filtered); start += chunkSize {
		end := start + chunkSize
		if end > len(filtered) {
			end = len(filtered)
		}
		chunk := filtered[start:end]

		var b strings.Builder
		b.Grow(128 + len(chunk)*32)
		b.WriteString("INSERT INTO cards (card_id, active, blocked, balance_cents, updated_at) VALUES ")

		args := make([]any, 0, len(chunk)*4)
		param := 1
		for i, r := range chunk {
			if i > 0 {
				b.WriteByte(',')
			}
			// ($1,$2,$3,$4,NOW()), ($5,$6,$7,$8,NOW()) ...
			b.WriteString(fmt.Sprintf("($%d,$%d,$%d,$%d,NOW())", param, param+1, param+2, param+3))
			param += 4
			args = append(args, r.CardID, r.Active, r.Blocked, r.BalanceCents)
		}
		b.WriteString(" ON CONFLICT (card_id) DO NOTHING;")

		if _, err := s.db.ExecContext(ctx, b.String(), args...); err != nil {
			return err
		}
	}

	return nil
}

func (s *Store) Validate(ctx context.Context, cardID string) (bool, string, error) {
	var active bool
	var blocked bool
	var balanceCents int64
	err := s.db.QueryRowContext(ctx, `SELECT active, blocked, balance_cents FROM cards WHERE card_id = $1`, cardID).Scan(&active, &blocked, &balanceCents)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, "unknown card", nil
		}
		return false, "db error", err
	}
	if blocked {
		return false, "blocked", nil
	}
	if !active {
		return false, "inactive", nil
	}
	if balanceCents <= 0 {
		return false, "insufficient funds", nil
	}
	return true, "ok", nil
}
