package carddb

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

type PAToken struct {
	Token  string
	CardID string
}

type PADebt struct {
	Token       string
	AmountCents int64
}

func (s *Store) EnsurePATables(ctx context.Context) error {
	q := `
		CREATE TABLE IF NOT EXISTS pctr_tokens (
			token TEXT PRIMARY KEY,
			card_id TEXT NOT NULL UNIQUE,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);

		CREATE TABLE IF NOT EXISTS pa_debts (
			id BIGSERIAL PRIMARY KEY,
			token TEXT NOT NULL,
			amount_cents BIGINT NOT NULL,
			received_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			exported_at TIMESTAMPTZ NULL
		);
		CREATE INDEX IF NOT EXISTS idx_pa_debts_outstanding ON pa_debts(exported_at, token);
	`
	_, err := s.db.ExecContext(ctx, q)
	return err
}

func (s *Store) GetOrCreateToken(ctx context.Context, cardID string) (string, error) {
	var token string
	err := s.db.QueryRowContext(ctx, `SELECT token FROM pctr_tokens WHERE card_id = $1`, cardID).Scan(&token)
	if err == nil {
		return token, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}

	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	token = hex.EncodeToString(b)

	_, err = s.db.ExecContext(ctx, `
		INSERT INTO pctr_tokens (token, card_id, created_at)
		VALUES ($1, $2, NOW())
		ON CONFLICT (card_id) DO NOTHING
	`, token, cardID)
	if err != nil {
		return "", err
	}

	// If a race inserted it, read again.
	err = s.db.QueryRowContext(ctx, `SELECT token FROM pctr_tokens WHERE card_id = $1`, cardID).Scan(&token)
	if err != nil {
		return "", err
	}
	return token, nil
}

func (s *Store) CardHasMinFunds(ctx context.Context, cardID string, minCents int64) (bool, string, error) {
	var active bool
	var blocked bool
	var balance int64
	err := s.db.QueryRowContext(ctx, `SELECT active, blocked, balance_cents FROM cards WHERE card_id = $1`, cardID).Scan(&active, &blocked, &balance)
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
	if balance < minCents {
		return false, "insufficient funds", nil
	}
	return true, "ok", nil
}

func (s *Store) InsertDebts(ctx context.Context, debts []PADebt) error {
	q := `INSERT INTO pa_debts (token, amount_cents, received_at) VALUES ($1, $2, NOW())`
	for _, d := range debts {
		if d.Token == "" || d.AmountCents == 0 {
			continue
		}
		if _, err := s.db.ExecContext(ctx, q, d.Token, d.AmountCents); err != nil {
			return err
		}
	}
	return nil
}

type RestitutionRow struct {
	CardID      string
	AmountCents int64
}

func (s *Store) BuildRestitution(ctx context.Context) ([]RestitutionRow, []int64, error) {
	// Aggregate outstanding debts by token then map to card_id.
	rows, err := s.db.QueryContext(ctx, `
		SELECT d.token, SUM(d.amount_cents) AS owed_cents, t.card_id
		FROM pa_debts d
		JOIN pctr_tokens t ON t.token = d.token
		WHERE d.exported_at IS NULL
		GROUP BY d.token, t.card_id
		ORDER BY t.card_id ASC
	`)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	byCard := map[string]int64{}
	for rows.Next() {
		var token string
		var owed int64
		var cardID string
		if err := rows.Scan(&token, &owed, &cardID); err != nil {
			return nil, nil, err
		}
		byCard[cardID] += owed
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}

	out := make([]RestitutionRow, 0, len(byCard))
	for cardID, owed := range byCard {
		out = append(out, RestitutionRow{CardID: cardID, AmountCents: owed})
	}

	// Capture ids of debts included so caller can mark exported.
	idRows, err := s.db.QueryContext(ctx, `SELECT id FROM pa_debts WHERE exported_at IS NULL`)
	if err != nil {
		return out, nil, err
	}
	defer idRows.Close()
	var ids []int64
	for idRows.Next() {
		var id int64
		if err := idRows.Scan(&id); err != nil {
			return out, nil, err
		}
		ids = append(ids, id)
	}
	return out, ids, nil
}

func (s *Store) MarkDebtsExported(ctx context.Context, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	// Chunk to keep query sizes reasonable.
	const chunk = 500
	for i := 0; i < len(ids); i += chunk {
		end := i + chunk
		if end > len(ids) {
			end = len(ids)
		}
		// Build a VALUES list.
		vals := ""
		args := make([]any, 0, end-i)
		for j, id := range ids[i:end] {
			if j > 0 {
				vals += ","
			}
			vals += fmt.Sprintf("($%d)", j+1)
			args = append(args, id)
		}
		q := fmt.Sprintf(`UPDATE pa_debts SET exported_at = $%d WHERE id IN (SELECT v.id FROM (VALUES %s) AS v(id))`, len(args)+1, vals)
		args = append(args, time.Now().UTC())
		if _, err := s.db.ExecContext(ctx, q, args...); err != nil {
			return err
		}
	}
	return nil
}
