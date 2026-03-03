package carddb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

type CSTransaction struct {
	TransactionID string
	CardID        string
	GateID        string
	StationID     string
	TapTime       time.Time
	Allowed       bool
	Reason        string
}

type StationCost struct {
	FromStation string
	ToStation   string
	CostCents   int64
}

func (s *Store) EnsureCSTables(ctx context.Context) error {
	q := `
		CREATE TABLE IF NOT EXISTS transactions (
			transaction_id TEXT PRIMARY KEY,
			card_id TEXT NOT NULL,
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
		CREATE INDEX IF NOT EXISTS idx_transactions_unprocessed ON transactions(card_id, processed, tap_time);

		CREATE TABLE IF NOT EXISTS station_costs (
			from_station TEXT NOT NULL,
			to_station TEXT NOT NULL,
			cost_cents BIGINT NOT NULL,
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			PRIMARY KEY(from_station, to_station)
		);
	`
	_, err := s.db.ExecContext(ctx, q)
	return err
}

func (s *Store) SeedStationCosts(ctx context.Context, costs []StationCost) error {
	q := `
		INSERT INTO station_costs (from_station, to_station, cost_cents, updated_at)
		VALUES ($1, $2, $3, NOW())
		ON CONFLICT (from_station, to_station) DO NOTHING;
	`
	for _, c := range costs {
		if c.FromStation == "" || c.ToStation == "" {
			continue
		}
		if c.CostCents < 0 {
			continue
		}
		if _, err := s.db.ExecContext(ctx, q, c.FromStation, c.ToStation, c.CostCents); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) InsertCSTransactions(ctx context.Context, txs []CSTransaction) error {
	q := `
		INSERT INTO transactions (
			transaction_id, card_id, gate_id, station_id, tap_time, allowed, reason, processed
		) VALUES ($1, $2, $3, $4, $5, $6, $7, FALSE)
		ON CONFLICT (transaction_id) DO NOTHING;
	`
	for _, tx := range txs {
		if tx.TransactionID == "" || tx.CardID == "" || tx.GateID == "" || tx.StationID == "" || tx.TapTime.IsZero() {
			continue
		}
		if _, err := s.db.ExecContext(ctx, q, tx.TransactionID, tx.CardID, tx.GateID, tx.StationID, tx.TapTime, tx.Allowed, tx.Reason); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) getStationCostCentsTx(ctx context.Context, tx *sql.Tx, a, b string) (int64, bool, error) {
	var cost int64
	err := tx.QueryRowContext(ctx, `
		SELECT cost_cents FROM station_costs
		WHERE (from_station = $1 AND to_station = $2) OR (from_station = $2 AND to_station = $1)
		LIMIT 1
	`, a, b).Scan(&cost)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, false, nil
		}
		return 0, false, err
	}
	return cost, true, nil
}

type unprocessedRow struct {
	id      string
	station string
	time    time.Time
}

type Ticket struct {
	ID         int64
	StationID  string
	TrainTime  time.Time
	Passenger  string
	CreatedAt  time.Time
	Active     bool
}

// RunJourneyConstruction processes unprocessed transactions per card in chronological order.
// It pairs transactions (0-1, 2-3, ...) and debits card balance by the station-to-station cost.
// If one transaction is left over, it remains unprocessed.
func (s *Store) RunJourneyConstruction(ctx context.Context) (int, error) {
	// Find cards with at least 2 unprocessed taps.
	rows, err := s.db.QueryContext(ctx, `
		SELECT card_id
		FROM transactions
		WHERE processed = FALSE
		GROUP BY card_id
		HAVING COUNT(*) >= 2
		ORDER BY card_id ASC
	`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	var cardIDs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return 0, err
		}
		cardIDs = append(cardIDs, id)
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}

	journeys := 0
	for _, cardID := range cardIDs {
		select {
		case <-ctx.Done():
			return journeys, ctx.Err()
		default:
		}

		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return journeys, err
		}

		// Lock the card row so debit is consistent.
		var curBal int64
		err = tx.QueryRowContext(ctx, `SELECT balance_cents FROM cards WHERE card_id = $1 FOR UPDATE`, cardID).Scan(&curBal)
		if err != nil {
			_ = tx.Rollback()
			if errors.Is(err, sql.ErrNoRows) {
				// card doesn't exist, mark its transactions as processed with error.
				_, _ = tx.ExecContext(ctx, `UPDATE transactions SET processed=TRUE, processed_at=NOW(), journey_cost_cents=0, error_reason='unknown card' WHERE card_id=$1 AND processed=FALSE`, cardID)
				_ = tx.Commit()
				continue
			}
			return journeys, err
		}

		// Lock the unprocessed tx rows.
		txRows, err := tx.QueryContext(ctx, `
			SELECT transaction_id, station_id, tap_time
			FROM transactions
			WHERE card_id = $1 AND processed = FALSE
			ORDER BY tap_time ASC
			FOR UPDATE
		`, cardID)
		if err != nil {
			_ = tx.Rollback()
			return journeys, err
		}
		var taps []unprocessedRow
		for txRows.Next() {
			var r unprocessedRow
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

		// Process pairs.
		for i := 0; i+1 < len(taps); i += 2 {
			from := taps[i]
			to := taps[i+1]

			cost, ok, err := s.getStationCostCentsTx(ctx, tx, from.station, to.station)
			if err != nil {
				_ = tx.Rollback()
				return journeys, err
			}
			if !ok {
				// Mark as processed but with no debit.
				if _, err := tx.ExecContext(ctx, `
					UPDATE transactions
					SET processed=TRUE, processed_at=NOW(), journey_cost_cents=0, error_reason='missing cost'
					WHERE transaction_id IN ($1, $2)
				`, from.id, to.id); err != nil {
					_ = tx.Rollback()
					return journeys, err
				}
				continue
			}

			if _, err := tx.ExecContext(ctx, `UPDATE cards SET balance_cents = balance_cents - $1, updated_at = NOW() WHERE card_id = $2`, cost, cardID); err != nil {
				_ = tx.Rollback()
				return journeys, err
			}
			if _, err := tx.ExecContext(ctx, `
				UPDATE transactions
				SET processed=TRUE, processed_at=NOW(), journey_cost_cents=$3, error_reason=NULL
				WHERE transaction_id IN ($1, $2)
			`, from.id, to.id, cost); err != nil {
				_ = tx.Rollback()
				return journeys, err
			}
			journeys++
		}

		if err := tx.Commit(); err != nil {
			return journeys, fmt.Errorf("commit: %w", err)
		}
	}
	return journeys, nil
}

// EnsureTicketTables creates tables used for ticket distribution.
func (s *Store) EnsureTicketTables(ctx context.Context) error {
	q := `
		CREATE TABLE IF NOT EXISTS tickets (
			id BIGSERIAL PRIMARY KEY,
			station_id TEXT NOT NULL,
			train_time TIMESTAMPTZ NOT NULL,
			passenger TEXT NOT NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			active BOOLEAN NOT NULL DEFAULT TRUE
		);
		CREATE INDEX IF NOT EXISTS idx_tickets_train_time ON tickets(train_time);
		CREATE INDEX IF NOT EXISTS idx_tickets_created_at ON tickets(created_at);
	`
	_, err := s.db.ExecContext(ctx, q)
	return err
}

func (s *Store) InsertTicket(ctx context.Context, t Ticket) (int64, error) {
	q := `INSERT INTO tickets (station_id, train_time, passenger, created_at, active) VALUES ($1,$2,$3,$4,$5) RETURNING id`
	var id int64
	created := t.CreatedAt
	if created.IsZero() {
		created = time.Now().UTC()
	}
	err := s.db.QueryRowContext(ctx, q, t.StationID, t.TrainTime, t.Passenger, created, t.Active).Scan(&id)
	if err != nil {
		return 0, err
	}
	return id, nil
}

// SeedTickets populates the tickets table with tickets for the next 15 minutes.
// Creates tickets at 1-minute intervals for each station.
func (s *Store) SeedTickets(ctx context.Context, stationIDs []string) error {
	if len(stationIDs) == 0 {
		return nil
	}
	now := time.Now().UTC()
	passengerNames := []string{"Alice", "Bob", "Charlie", "Diana", "Eve", "Frank", "Grace", "Hank", "Ivy", "Jack"}
	var tickets []Ticket
	// Generate tickets for next 15 minutes at 1-minute intervals
	for min := 1; min <= 15; min++ {
		trainTime := now.Add(time.Duration(min) * time.Minute)
		for _, stationID := range stationIDs {
			passenger := passengerNames[(min+len(stationID))%len(passengerNames)]
			tickets = append(tickets, Ticket{
				StationID:  stationID,
				TrainTime:  trainTime,
				Passenger:  fmt.Sprintf("%s-%d", passenger, min),
				CreatedAt:  now,
				Active:     true,
			})
		}
	}
	for _, t := range tickets {
		if _, err := s.InsertTicket(ctx, t); err != nil {
			return err
		}
	}
	return nil
}

// GetActiveTicketsWithin returns tickets whose train_time is between now and now+window and active=true.
func (s *Store) GetActiveTicketsWithin(ctx context.Context, now time.Time, window time.Duration) ([]Ticket, error) {
	q := `SELECT id, station_id, train_time, passenger, created_at, active FROM tickets WHERE active = TRUE AND train_time >= $1 AND train_time <= $2 ORDER BY station_id ASC`
	rows, err := s.db.QueryContext(ctx, q, now.UTC(), now.Add(window).UTC())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Ticket
	for rows.Next() {
		var t Ticket
		if err := rows.Scan(&t.ID, &t.StationID, &t.TrainTime, &t.Passenger, &t.CreatedAt, &t.Active); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// GetTicketsCreatedSince returns tickets created after the given time (inclusive).
func (s *Store) GetTicketsCreatedSince(ctx context.Context, since time.Time) ([]Ticket, error) {
	q := `SELECT id, station_id, train_time, passenger, created_at, active FROM tickets WHERE created_at > $1 ORDER BY created_at ASC`
	rows, err := s.db.QueryContext(ctx, q, since.UTC())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Ticket
	for rows.Next() {
		var t Ticket
		if err := rows.Scan(&t.ID, &t.StationID, &t.TrainTime, &t.Passenger, &t.CreatedAt, &t.Active); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// Manifest represents a ticket manifest generated by CS for distribution.
type Manifest struct {
	ID        string    `json:"id"`
	Generated time.Time `json:"generated_at"`
	Payload   string    `json:"payload_json"` // opaque JSON payload (list of tickets)
}

// EnsureManifestTables creates the manifests table used by distributors to poll CS.
func (s *Store) EnsureManifestTables(ctx context.Context) error {
	q := `
		CREATE TABLE IF NOT EXISTS manifests (
			id TEXT PRIMARY KEY,
			generated_at TIMESTAMPTZ NOT NULL,
			payload_json JSONB NOT NULL
		);
		CREATE INDEX IF NOT EXISTS idx_manifests_generated_at ON manifests(generated_at);
	`
	_, err := s.db.ExecContext(ctx, q)
	return err
}

// InsertManifest stores a manifest payload into the DB. payload should be valid JSON string.
func (s *Store) InsertManifest(ctx context.Context, id string, generated time.Time, payload string) error {
	q := `INSERT INTO manifests (id, generated_at, payload_json) VALUES ($1, $2, $3) ON CONFLICT (id) DO NOTHING`
	_, err := s.db.ExecContext(ctx, q, id, generated.UTC(), payload)
	return err
}

// GetManifestsSince returns manifests generated after the given time, ordered by generated_at.
func (s *Store) GetManifestsSince(ctx context.Context, since time.Time) ([]Manifest, error) {
	q := `SELECT id, generated_at, payload_json FROM manifests WHERE generated_at > $1 ORDER BY generated_at ASC`
	rows, err := s.db.QueryContext(ctx, q, since.UTC())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Manifest
	for rows.Next() {
		var m Manifest
		var payloadBytes []byte
		if err := rows.Scan(&m.ID, &m.Generated, &payloadBytes); err != nil {
			return nil, err
		}
		m.Payload = string(payloadBytes)
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// GetManifestByID returns a single manifest by id.
func (s *Store) GetManifestByID(ctx context.Context, id string) (*Manifest, error) {
	q := `SELECT id, generated_at, payload_json FROM manifests WHERE id = $1 LIMIT 1`
	var m Manifest
	var payloadBytes []byte
	if err := s.db.QueryRowContext(ctx, q, id).Scan(&m.ID, &m.Generated, &payloadBytes); err != nil {
		return nil, err
	}
	m.Payload = string(payloadBytes)
	return &m, nil
}
