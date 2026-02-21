package carddb

import (
	"context"
	"time"
)

type PCTRTransaction struct {
	TransactionID string
	Token         string
	GateID        string
	StationID     string
	TapTime       time.Time
	Allowed       bool
	Reason        string
}

type PCTROwed struct {
	Token       string
	AmountCents int64
}

func (s *Store) EnsurePCTRTables(ctx context.Context) error {
	q := `
		CREATE TABLE IF NOT EXISTS pctr_transactions (
			transaction_id TEXT PRIMARY KEY,
			token TEXT NOT NULL,
			gate_id TEXT NOT NULL,
			station_id TEXT NOT NULL,
			tap_time TIMESTAMPTZ NOT NULL,
			allowed BOOLEAN NOT NULL,
			reason TEXT NOT NULL,
			processed BOOLEAN NOT NULL DEFAULT FALSE,
			processed_at TIMESTAMPTZ NULL,
			journey_cost_cents BIGINT NULL,
			error_reason TEXT NULL
		);
		CREATE INDEX IF NOT EXISTS idx_pctr_unprocessed ON pctr_transactions(token, processed, tap_time);

		CREATE TABLE IF NOT EXISTS station_costs (
			from_station TEXT NOT NULL,
			to_station TEXT NOT NULL,
			cost_cents BIGINT NOT NULL,
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			PRIMARY KEY(from_station, to_station)
		);

		CREATE TABLE IF NOT EXISTS pctr_debts (
			id BIGSERIAL PRIMARY KEY,
			token TEXT NOT NULL,
			amount_cents BIGINT NOT NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			reported BOOLEAN NOT NULL DEFAULT FALSE,
			reported_at TIMESTAMPTZ NULL
		);
		CREATE INDEX IF NOT EXISTS idx_pctr_debts_unreported ON pctr_debts(reported, token);
	`
	_, err := s.db.ExecContext(ctx, q)
	return err
}

func (s *Store) InsertPCTRTransactions(ctx context.Context, txs []PCTRTransaction) error {
	q := `
		INSERT INTO pctr_transactions (
			transaction_id, token, gate_id, station_id, tap_time, allowed, reason, processed
		) VALUES ($1, $2, $3, $4, $5, $6, $7, FALSE)
		ON CONFLICT (transaction_id) DO NOTHING;
	`
	for _, tx := range txs {
		if tx.TransactionID == "" || tx.Token == "" || tx.GateID == "" || tx.StationID == "" || tx.TapTime.IsZero() {
			continue
		}
		if _, err := s.db.ExecContext(ctx, q, tx.TransactionID, tx.Token, tx.GateID, tx.StationID, tx.TapTime, tx.Allowed, tx.Reason); err != nil {
			return err
		}
	}
	return nil
}

type pctrTap struct {
	id      string
	station string
	time    time.Time
}

// RunPCTRJourneyConstruction processes unprocessed token-based transactions and creates debt rows.
// Returns journeys processed.
func (s *Store) RunPCTRJourneyConstruction(ctx context.Context) (int, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT token
		FROM pctr_transactions
		WHERE processed = FALSE
		GROUP BY token
		HAVING COUNT(*) >= 2
		ORDER BY token ASC
	`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	var tokens []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return 0, err
		}
		tokens = append(tokens, t)
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}

	journeys := 0
	for _, token := range tokens {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return journeys, err
		}

		txRows, err := tx.QueryContext(ctx, `
			SELECT transaction_id, station_id, tap_time
			FROM pctr_transactions
			WHERE token = $1 AND processed = FALSE
			ORDER BY tap_time ASC
			FOR UPDATE
		`, token)
		if err != nil {
			_ = tx.Rollback()
			return journeys, err
		}
		var taps []pctrTap
		for txRows.Next() {
			var r pctrTap
			if err := txRows.Scan(&r.id, &r.station, &r.time); err != nil {
				_ = txRows.Close()
				_ = tx.Rollback()
				return journeys, err
			}
			taps = append(taps, r)
		}
		_ = txRows.Close()
		if len(taps) < 2 {
			_ = tx.Rollback()
			continue
		}

		for i := 0; i+1 < len(taps); i += 2 {
			from := taps[i]
			to := taps[i+1]
			cost, ok, err := s.getStationCostCentsTx(ctx, tx, from.station, to.station)
			if err != nil {
				_ = tx.Rollback()
				return journeys, err
			}
			if !ok {
				if _, err := tx.ExecContext(ctx, `
					UPDATE pctr_transactions
					SET processed=TRUE, processed_at=NOW(), journey_cost_cents=0, error_reason='missing cost'
					WHERE transaction_id IN ($1, $2)
				`, from.id, to.id); err != nil {
					_ = tx.Rollback()
					return journeys, err
				}
				continue
			}

			if _, err := tx.ExecContext(ctx, `
				INSERT INTO pctr_debts (token, amount_cents, created_at, reported)
				VALUES ($1, $2, NOW(), FALSE)
			`, token, cost); err != nil {
				_ = tx.Rollback()
				return journeys, err
			}

			if _, err := tx.ExecContext(ctx, `
				UPDATE pctr_transactions
				SET processed=TRUE, processed_at=NOW(), journey_cost_cents=$3, error_reason=NULL
				WHERE transaction_id IN ($1, $2)
			`, from.id, to.id, cost); err != nil {
				_ = tx.Rollback()
				return journeys, err
			}
			journeys++
		}

		if err := tx.Commit(); err != nil {
			return journeys, err
		}
	}
	return journeys, nil
}

func (s *Store) GetUnreportedDebts(ctx context.Context) ([]PCTROwed, []int64, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT token, SUM(amount_cents) AS owed
		FROM pctr_debts
		WHERE reported = FALSE
		GROUP BY token
		ORDER BY token ASC
	`)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	var out []PCTROwed
	for rows.Next() {
		var t string
		var owed int64
		if err := rows.Scan(&t, &owed); err != nil {
			return nil, nil, err
		}
		out = append(out, PCTROwed{Token: t, AmountCents: owed})
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}

	idRows, err := s.db.QueryContext(ctx, `SELECT id FROM pctr_debts WHERE reported = FALSE`)
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

func (s *Store) MarkDebtsReported(ctx context.Context, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `UPDATE pctr_debts SET reported=TRUE, reported_at=NOW() WHERE reported=FALSE`)
	return err
}
