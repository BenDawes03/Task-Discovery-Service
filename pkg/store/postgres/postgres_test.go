package postgres

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"

	"tds/pkg/store"
)

func newMockStore(t *testing.T) (*PostgresStore, sqlmock.Sqlmock, *sql.DB) {
	t.Helper()

	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("failed to create sqlmock: %v", err)
	}

	ps := &PostgresStore{
		db:              db,
		roundRobinIndex: make(map[string]int),
	}

	return ps, mock, db
}

func TestRegister_WithQueryCountSync(t *testing.T) {
	ps, mock, db := newMockStore(t)
	defer db.Close()

	now := time.Now().UTC()
	entry := &store.ServiceEntry{
		Address:       "10.0.0.1:9000",
		LastHeartbeat: now,
		QueryCount:    7,
		Capacity:      3,
	}

	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO services")).
		WithArgs("task-a", entry.Address, entry.LastHeartbeat, entry.QueryCount, entry.Capacity).
		WillReturnResult(sqlmock.NewResult(1, 1))

	if err := ps.Register(context.Background(), "task-a", entry); err != nil {
		t.Fatalf("Register returned error: %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestRegister_NormalRegistrationNormalizesCapacity(t *testing.T) {
	ps, mock, db := newMockStore(t)
	defer db.Close()

	now := time.Now().UTC()
	entry := &store.ServiceEntry{
		Address:       "10.0.0.2:9000",
		LastHeartbeat: now,
		QueryCount:    0,
		Capacity:      0,
	}

	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO services")).
		WithArgs("task-a", entry.Address, entry.LastHeartbeat, 1).
		WillReturnResult(sqlmock.NewResult(1, 1))

	if err := ps.Register(context.Background(), "task-a", entry); err != nil {
		t.Fatalf("Register returned error: %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestRegister_ExecError(t *testing.T) {
	ps, mock, db := newMockStore(t)
	defer db.Close()

	now := time.Now().UTC()
	entry := &store.ServiceEntry{Address: "10.0.0.3:9000", LastHeartbeat: now, Capacity: 2}

	expected := errors.New("insert failed")
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO services")).
		WithArgs("task-a", entry.Address, entry.LastHeartbeat, entry.Capacity).
		WillReturnError(expected)

	err := ps.Register(context.Background(), "task-a", entry)
	if !errors.Is(err, expected) {
		t.Fatalf("expected %v, got %v", expected, err)
	}
}

func TestGetService_QueryError(t *testing.T) {
	ps, mock, db := newMockStore(t)
	defer db.Close()

	expected := errors.New("select failed")
	mock.ExpectQuery(regexp.QuoteMeta("SELECT address, last_heartbeat, query_count, capacity")).
		WithArgs("task-a").
		WillReturnError(expected)

	_, err := ps.GetService(context.Background(), "task-a")
	if !errors.Is(err, expected) {
		t.Fatalf("expected %v, got %v", expected, err)
	}
}

func TestGetService_NoEntriesReturnsNotFound(t *testing.T) {
	ps, mock, db := newMockStore(t)
	defer db.Close()

	rows := sqlmock.NewRows([]string{"address", "last_heartbeat", "query_count", "capacity"})
	mock.ExpectQuery(regexp.QuoteMeta("SELECT address, last_heartbeat, query_count, capacity")).
		WithArgs("task-a").
		WillReturnRows(rows)

	_, err := ps.GetService(context.Background(), "task-a")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestGetService_ScanError(t *testing.T) {
	ps, mock, db := newMockStore(t)
	defer db.Close()

	rows := sqlmock.NewRows([]string{"address", "last_heartbeat", "query_count", "capacity"}).
		AddRow("10.0.0.1:9000", "bad-time", int64(1), 1)

	mock.ExpectQuery(regexp.QuoteMeta("SELECT address, last_heartbeat, query_count, capacity")).
		WithArgs("task-a").
		WillReturnRows(rows)

	_, err := ps.GetService(context.Background(), "task-a")
	if err == nil {
		t.Fatal("expected scan error, got nil")
	}
}

func TestGetService_RowsErr(t *testing.T) {
	ps, mock, db := newMockStore(t)
	defer db.Close()

	now := time.Now().UTC()
	rows := sqlmock.NewRows([]string{"address", "last_heartbeat", "query_count", "capacity"}).
		AddRow("10.0.0.1:9000", now, int64(1), 1).
		RowError(0, errors.New("row error"))

	mock.ExpectQuery(regexp.QuoteMeta("SELECT address, last_heartbeat, query_count, capacity")).
		WithArgs("task-a").
		WillReturnRows(rows)

	_, err := ps.GetService(context.Background(), "task-a")
	if err == nil {
		t.Fatal("expected rows error, got nil")
	}
}

func TestGetService_RoundRobinAndIncrement(t *testing.T) {
	ps, mock, db := newMockStore(t)
	defer db.Close()

	now := time.Now().UTC()
	rowsFirst := sqlmock.NewRows([]string{"address", "last_heartbeat", "query_count", "capacity"}).
		AddRow("10.0.0.1:9000", now, int64(5), 1).
		AddRow("10.0.0.2:9000", now, int64(9), 1)

	rowsSecond := sqlmock.NewRows([]string{"address", "last_heartbeat", "query_count", "capacity"}).
		AddRow("10.0.0.1:9000", now, int64(6), 1).
		AddRow("10.0.0.2:9000", now, int64(9), 1)

	mock.ExpectQuery(regexp.QuoteMeta("SELECT address, last_heartbeat, query_count, capacity")).WithArgs("task-a").WillReturnRows(rowsFirst)
	mock.ExpectExec(regexp.QuoteMeta("UPDATE services")).WithArgs("task-a", "10.0.0.1:9000").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT address, last_heartbeat, query_count, capacity")).WithArgs("task-a").WillReturnRows(rowsSecond)
	mock.ExpectExec(regexp.QuoteMeta("UPDATE services")).WithArgs("task-a", "10.0.0.2:9000").WillReturnResult(sqlmock.NewResult(0, 1))

	first, err := ps.GetService(context.Background(), "task-a")
	if err != nil {
		t.Fatalf("first GetService error: %v", err)
	}
	if first.Address != "10.0.0.1:9000" || first.QueryCount != 6 {
		t.Fatalf("unexpected first selection: %+v", first)
	}

	second, err := ps.GetService(context.Background(), "task-a")
	if err != nil {
		t.Fatalf("second GetService error: %v", err)
	}
	if second.Address != "10.0.0.2:9000" || second.QueryCount != 10 {
		t.Fatalf("unexpected second selection: %+v", second)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestGetService_UsesCumulativeCapacityWeights(t *testing.T) {
	ps, mock, db := newMockStore(t)
	defer db.Close()

	now := time.Now().UTC()
	makeRows := func() *sqlmock.Rows {
		return sqlmock.NewRows([]string{"address", "last_heartbeat", "query_count", "capacity"}).
			AddRow("10.0.0.1:9000", now, int64(1), 3).
			AddRow("10.0.0.2:9000", now.Add(-5*time.Minute), int64(2), 1)
	}

	for _, addr := range []string{"10.0.0.1:9000", "10.0.0.1:9000", "10.0.0.1:9000", "10.0.0.2:9000"} {
		mock.ExpectQuery(regexp.QuoteMeta("SELECT address, last_heartbeat, query_count, capacity")).WithArgs("task-a").WillReturnRows(makeRows())
		mock.ExpectExec(regexp.QuoteMeta("UPDATE services")).WithArgs("task-a", addr).WillReturnResult(sqlmock.NewResult(0, 1))
	}

	counts := map[string]int{}
	for i := 0; i < 4; i++ {
		entry, err := ps.GetService(context.Background(), "task-a")
		if err != nil {
			t.Fatalf("GetService iteration %d returned error: %v", i, err)
		}
		counts[entry.Address]++
	}

	if counts["10.0.0.1:9000"] != 3 || counts["10.0.0.2:9000"] != 1 {
		t.Fatalf("expected 3:1 capacity-weighted split, got %+v", counts)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestGetService_UpdateError(t *testing.T) {
	ps, mock, db := newMockStore(t)
	defer db.Close()

	now := time.Now().UTC()
	rows := sqlmock.NewRows([]string{"address", "last_heartbeat", "query_count", "capacity"}).
		AddRow("10.0.0.1:9000", now, int64(1), 1)

	expected := errors.New("update failed")
	mock.ExpectQuery(regexp.QuoteMeta("SELECT address, last_heartbeat, query_count, capacity")).WithArgs("task-a").WillReturnRows(rows)
	mock.ExpectExec(regexp.QuoteMeta("UPDATE services")).WithArgs("task-a", "10.0.0.1:9000").WillReturnError(expected)

	_, err := ps.GetService(context.Background(), "task-a")
	if !errors.Is(err, expected) {
		t.Fatalf("expected %v, got %v", expected, err)
	}
}

func TestListServices_Success(t *testing.T) {
	ps, mock, db := newMockStore(t)
	defer db.Close()

	now := time.Now().UTC()
	rows := sqlmock.NewRows([]string{"task", "address", "last_heartbeat", "query_count", "capacity"}).
		AddRow("task-a", "10.0.0.1:9000", now, int64(2), 3).
		AddRow("task-a", "10.0.0.2:9000", now, int64(4), 0).
		AddRow("task-b", "10.0.1.1:9000", now, int64(1), -2)

	mock.ExpectQuery(regexp.QuoteMeta("SELECT task, address, last_heartbeat, query_count, capacity")).WillReturnRows(rows)

	got, err := ps.ListServices(context.Background())
	if err != nil {
		t.Fatalf("ListServices error: %v", err)
	}

	if len(got["task-a"]) != 2 || len(got["task-b"]) != 1 {
		t.Fatalf("unexpected grouping: %+v", got)
	}
	if got["task-a"][1].Capacity != 1 || got["task-b"][0].Capacity != 1 {
		t.Fatalf("expected normalized capacity to 1 for non-positive values, got %+v", got)
	}
}

func TestListServices_QueryError(t *testing.T) {
	ps, mock, db := newMockStore(t)
	defer db.Close()

	expected := errors.New("query failed")
	mock.ExpectQuery(regexp.QuoteMeta("SELECT task, address, last_heartbeat, query_count, capacity")).WillReturnError(expected)

	_, err := ps.ListServices(context.Background())
	if !errors.Is(err, expected) {
		t.Fatalf("expected %v, got %v", expected, err)
	}
}

func TestListServices_ScanError(t *testing.T) {
	ps, mock, db := newMockStore(t)
	defer db.Close()

	rows := sqlmock.NewRows([]string{"task", "address", "last_heartbeat", "query_count", "capacity"}).
		AddRow("task-a", "10.0.0.1:9000", "bad-time", int64(2), 1)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT task, address, last_heartbeat, query_count, capacity")).WillReturnRows(rows)

	_, err := ps.ListServices(context.Background())
	if err == nil {
		t.Fatal("expected scan error, got nil")
	}
}

func TestListServices_RowsErr(t *testing.T) {
	ps, mock, db := newMockStore(t)
	defer db.Close()

	now := time.Now().UTC()
	rows := sqlmock.NewRows([]string{"task", "address", "last_heartbeat", "query_count", "capacity"}).
		AddRow("task-a", "10.0.0.1:9000", now, int64(2), 1).
		RowError(0, errors.New("row error"))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT task, address, last_heartbeat, query_count, capacity")).WillReturnRows(rows)

	_, err := ps.ListServices(context.Background())
	if err == nil {
		t.Fatal("expected rows error, got nil")
	}
}

func TestListTaskServices_Success(t *testing.T) {
	ps, mock, db := newMockStore(t)
	defer db.Close()

	now := time.Now().UTC()
	rows := sqlmock.NewRows([]string{"address", "last_heartbeat", "query_count", "capacity"}).
		AddRow("10.0.0.1:9000", now, int64(2), 3).
		AddRow("10.0.0.2:9000", now, int64(4), 0)

	mock.ExpectQuery(regexp.QuoteMeta("SELECT address, last_heartbeat, query_count, capacity")).
		WithArgs("task-a").
		WillReturnRows(rows)

	got, err := ps.ListTaskServices(context.Background(), "task-a")
	if err != nil {
		t.Fatalf("ListTaskServices error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(got))
	}
	if got[1].Capacity != 1 {
		t.Fatalf("expected normalized capacity to 1 for non-positive values, got %d", got[1].Capacity)
	}
}

func TestListTaskServices_QueryError(t *testing.T) {
	ps, mock, db := newMockStore(t)
	defer db.Close()

	expected := errors.New("query failed")
	mock.ExpectQuery(regexp.QuoteMeta("SELECT address, last_heartbeat, query_count, capacity")).
		WithArgs("task-a").
		WillReturnError(expected)

	_, err := ps.ListTaskServices(context.Background(), "task-a")
	if !errors.Is(err, expected) {
		t.Fatalf("expected %v, got %v", expected, err)
	}
}

func TestListTaskServices_ScanError(t *testing.T) {
	ps, mock, db := newMockStore(t)
	defer db.Close()

	rows := sqlmock.NewRows([]string{"address", "last_heartbeat", "query_count", "capacity"}).
		AddRow("10.0.0.1:9000", "bad-time", int64(2), 1)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT address, last_heartbeat, query_count, capacity")).
		WithArgs("task-a").
		WillReturnRows(rows)

	_, err := ps.ListTaskServices(context.Background(), "task-a")
	if err == nil {
		t.Fatal("expected scan error, got nil")
	}
}

func TestListTopTasksByQueryCount_Success(t *testing.T) {
	ps, mock, db := newMockStore(t)
	defer db.Close()

	rows := sqlmock.NewRows([]string{"task"}).
		AddRow("task-hot").
		AddRow("task-warm")

	mock.ExpectQuery(regexp.QuoteMeta("SELECT task")).
		WithArgs(2).
		WillReturnRows(rows)

	tasks, err := ps.ListTopTasksByQueryCount(context.Background(), 2)
	if err != nil {
		t.Fatalf("ListTopTasksByQueryCount error: %v", err)
	}
	if len(tasks) != 2 || tasks[0] != "task-hot" || tasks[1] != "task-warm" {
		t.Fatalf("unexpected top task ordering: %v", tasks)
	}
}

func TestListTopTasksByQueryCount_QueryError(t *testing.T) {
	ps, mock, db := newMockStore(t)
	defer db.Close()

	expected := errors.New("top tasks query failed")
	mock.ExpectQuery(regexp.QuoteMeta("SELECT task")).
		WithArgs(3).
		WillReturnError(expected)

	_, err := ps.ListTopTasksByQueryCount(context.Background(), 3)
	if !errors.Is(err, expected) {
		t.Fatalf("expected %v, got %v", expected, err)
	}
}

func TestListServicesForTasks_Success(t *testing.T) {
	ps, mock, db := newMockStore(t)
	defer db.Close()

	now := time.Now().UTC()
	rows := sqlmock.NewRows([]string{"task", "address", "last_heartbeat", "query_count", "capacity"}).
		AddRow("task-a", "10.0.0.1:9000", now, int64(3), 2).
		AddRow("task-b", "10.0.0.2:9000", now, int64(1), 0)

	mock.ExpectQuery(regexp.QuoteMeta("SELECT task, address, last_heartbeat, query_count, capacity")).
		WithArgs("task-a", "task-b").
		WillReturnRows(rows)

	result, err := ps.ListServicesForTasks(context.Background(), []string{"task-a", "task-b"})
	if err != nil {
		t.Fatalf("ListServicesForTasks error: %v", err)
	}
	if len(result["task-a"]) != 1 || len(result["task-b"]) != 1 {
		t.Fatalf("unexpected grouped result: %+v", result)
	}
	if result["task-b"][0].Capacity != 1 {
		t.Fatalf("expected normalized capacity for non-positive values, got %d", result["task-b"][0].Capacity)
	}
}

func TestListServicesForTasks_QueryError(t *testing.T) {
	ps, mock, db := newMockStore(t)
	defer db.Close()

	expected := errors.New("subset query failed")
	mock.ExpectQuery(regexp.QuoteMeta("SELECT task, address, last_heartbeat, query_count, capacity")).
		WithArgs("task-a").
		WillReturnError(expected)

	_, err := ps.ListServicesForTasks(context.Background(), []string{"task-a"})
	if !errors.Is(err, expected) {
		t.Fatalf("expected %v, got %v", expected, err)
	}
}

func TestCleanup_Success(t *testing.T) {
	ps, mock, db := newMockStore(t)
	defer db.Close()

	mock.ExpectExec(regexp.QuoteMeta("UPDATE services")).WithArgs(sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(0, 3))

	count, err := ps.Cleanup(context.Background(), 30*time.Second)
	if err != nil {
		t.Fatalf("Cleanup error: %v", err)
	}
	if count != 3 {
		t.Fatalf("expected 3 rows affected, got %d", count)
	}
}

func TestCleanup_ExecError(t *testing.T) {
	ps, mock, db := newMockStore(t)
	defer db.Close()

	expected := errors.New("cleanup failed")
	mock.ExpectExec(regexp.QuoteMeta("UPDATE services")).WithArgs(sqlmock.AnyArg()).WillReturnError(expected)

	_, err := ps.Cleanup(context.Background(), 30*time.Second)
	if !errors.Is(err, expected) {
		t.Fatalf("expected %v, got %v", expected, err)
	}
}

func TestCleanup_RowsAffectedError(t *testing.T) {
	ps, mock, db := newMockStore(t)
	defer db.Close()

	expected := errors.New("rows affected failed")
	mock.ExpectExec(regexp.QuoteMeta("UPDATE services")).WithArgs(sqlmock.AnyArg()).WillReturnResult(sqlmock.NewErrorResult(expected))

	_, err := ps.Cleanup(context.Background(), 30*time.Second)
	if !errors.Is(err, expected) {
		t.Fatalf("expected %v, got %v", expected, err)
	}
}

func TestMigrate_Success(t *testing.T) {
	ps, mock, db := newMockStore(t)
	defer db.Close()

	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("CREATE TABLE IF NOT EXISTS services")).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta("ALTER TABLE services ADD COLUMN IF NOT EXISTS query_count BIGINT NOT NULL DEFAULT 0;")).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta("ALTER TABLE services ADD COLUMN IF NOT EXISTS capacity INTEGER NOT NULL DEFAULT 1;")).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta("ALTER TABLE services ADD COLUMN IF NOT EXISTS is_active BOOLEAN NOT NULL DEFAULT TRUE;")).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta("ALTER TABLE services ADD COLUMN IF NOT EXISTS created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW();")).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta("ALTER TABLE services ADD COLUMN IF NOT EXISTS updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW();")).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta("CREATE UNIQUE INDEX IF NOT EXISTS idx_services_task_address_unique ON services(task, address);")).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta("CREATE INDEX IF NOT EXISTS idx_services_task ON services(task);")).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta("CREATE INDEX IF NOT EXISTS idx_services_last_heartbeat ON services(last_heartbeat);")).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta("CREATE INDEX IF NOT EXISTS idx_services_is_active ON services(is_active);")).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectCommit()

	if err := ps.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate error: %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestMigrate_BeginError(t *testing.T) {
	ps, mock, db := newMockStore(t)
	defer db.Close()

	expected := errors.New("begin failed")
	mock.ExpectBegin().WillReturnError(expected)

	err := ps.Migrate(context.Background())
	if !errors.Is(err, expected) {
		t.Fatalf("expected %v, got %v", expected, err)
	}
}

func TestMigrate_ExecErrorRollsBack(t *testing.T) {
	ps, mock, db := newMockStore(t)
	defer db.Close()

	expected := errors.New("ddl failed")
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("CREATE TABLE IF NOT EXISTS services")).WillReturnError(expected)
	mock.ExpectRollback()

	err := ps.Migrate(context.Background())
	if !errors.Is(err, expected) {
		t.Fatalf("expected %v, got %v", expected, err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestMigrate_CommitError(t *testing.T) {
	ps, mock, db := newMockStore(t)
	defer db.Close()

	expected := errors.New("commit failed")
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("CREATE TABLE IF NOT EXISTS services")).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta("ALTER TABLE services ADD COLUMN IF NOT EXISTS query_count BIGINT NOT NULL DEFAULT 0;")).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta("ALTER TABLE services ADD COLUMN IF NOT EXISTS capacity INTEGER NOT NULL DEFAULT 1;")).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta("ALTER TABLE services ADD COLUMN IF NOT EXISTS is_active BOOLEAN NOT NULL DEFAULT TRUE;")).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta("ALTER TABLE services ADD COLUMN IF NOT EXISTS created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW();")).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta("ALTER TABLE services ADD COLUMN IF NOT EXISTS updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW();")).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta("CREATE UNIQUE INDEX IF NOT EXISTS idx_services_task_address_unique ON services(task, address);")).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta("CREATE INDEX IF NOT EXISTS idx_services_task ON services(task);")).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta("CREATE INDEX IF NOT EXISTS idx_services_last_heartbeat ON services(last_heartbeat);")).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta("CREATE INDEX IF NOT EXISTS idx_services_is_active ON services(is_active);")).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectCommit().WillReturnError(expected)

	err := ps.Migrate(context.Background())
	if !errors.Is(err, expected) {
		t.Fatalf("expected %v, got %v", expected, err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestClose(t *testing.T) {
	ps, mock, db := newMockStore(t)
	mock.ExpectClose()

	if err := ps.Close(); err != nil {
		t.Fatalf("Close error: %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}

	_ = db
}

func TestListInactiveServices_Success(t *testing.T) {
	ps, mock, db := newMockStore(t)
	defer db.Close()

	now := time.Now().UTC()
	rows := sqlmock.NewRows([]string{"task", "address", "last_heartbeat", "query_count", "capacity"}).
		AddRow("task-a", "10.0.0.1:9000", now, int64(2), 0).
		AddRow("task-b", "10.0.1.1:9000", now, int64(1), 2)

	mock.ExpectQuery(regexp.QuoteMeta("SELECT task, address, last_heartbeat, query_count, capacity")).WillReturnRows(rows)

	got, err := ps.ListInactiveServices(context.Background())
	if err != nil {
		t.Fatalf("ListInactiveServices error: %v", err)
	}
	if len(got["task-a"]) != 1 || len(got["task-b"]) != 1 {
		t.Fatalf("unexpected grouping: %+v", got)
	}
	if got["task-a"][0].Capacity != 1 {
		t.Fatalf("expected normalized capacity for inactive service, got %+v", got)
	}
}

func TestListInactiveServices_QueryError(t *testing.T) {
	ps, mock, db := newMockStore(t)
	defer db.Close()

	expected := errors.New("query failed")
	mock.ExpectQuery(regexp.QuoteMeta("SELECT task, address, last_heartbeat, query_count, capacity")).WillReturnError(expected)

	_, err := ps.ListInactiveServices(context.Background())
	if !errors.Is(err, expected) {
		t.Fatalf("expected %v, got %v", expected, err)
	}
}

func TestListInactiveServices_ScanError(t *testing.T) {
	ps, mock, db := newMockStore(t)
	defer db.Close()

	rows := sqlmock.NewRows([]string{"task", "address", "last_heartbeat", "query_count", "capacity"}).
		AddRow("task-a", "10.0.0.1:9000", "bad-time", int64(2), 1)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT task, address, last_heartbeat, query_count, capacity")).WillReturnRows(rows)

	_, err := ps.ListInactiveServices(context.Background())
	if err == nil {
		t.Fatal("expected scan error, got nil")
	}
}

func TestListInactiveServices_RowsErr(t *testing.T) {
	ps, mock, db := newMockStore(t)
	defer db.Close()

	now := time.Now().UTC()
	rows := sqlmock.NewRows([]string{"task", "address", "last_heartbeat", "query_count", "capacity"}).
		AddRow("task-a", "10.0.0.1:9000", now, int64(2), 1).
		RowError(0, errors.New("row error"))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT task, address, last_heartbeat, query_count, capacity")).WillReturnRows(rows)

	_, err := ps.ListInactiveServices(context.Background())
	if err == nil {
		t.Fatal("expected rows error, got nil")
	}
}

func TestGetServiceHistory_SuccessAndNormalization(t *testing.T) {
	ps, mock, db := newMockStore(t)
	defer db.Close()

	now := time.Now().UTC()
	rows := sqlmock.NewRows([]string{"address", "last_heartbeat", "query_count", "capacity", "is_active"}).
		AddRow("10.0.0.1:9000", now, int64(2), 0, true).
		AddRow("10.0.0.2:9000", now.Add(-time.Minute), int64(7), 5, false)

	mock.ExpectQuery(regexp.QuoteMeta("SELECT address, last_heartbeat, query_count, capacity, is_active")).
		WithArgs("task-a").
		WillReturnRows(rows)

	got, err := ps.GetServiceHistory(context.Background(), "task-a")
	if err != nil {
		t.Fatalf("GetServiceHistory error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(got))
	}
	if got[0].Capacity != 1 {
		t.Fatalf("expected normalized first capacity to 1, got %d", got[0].Capacity)
	}
}

func TestGetServiceHistory_QueryError(t *testing.T) {
	ps, mock, db := newMockStore(t)
	defer db.Close()

	expected := errors.New("query failed")
	mock.ExpectQuery(regexp.QuoteMeta("SELECT address, last_heartbeat, query_count, capacity, is_active")).
		WithArgs("task-a").
		WillReturnError(expected)

	_, err := ps.GetServiceHistory(context.Background(), "task-a")
	if !errors.Is(err, expected) {
		t.Fatalf("expected %v, got %v", expected, err)
	}
}

func TestGetServiceHistory_ScanError(t *testing.T) {
	ps, mock, db := newMockStore(t)
	defer db.Close()

	rows := sqlmock.NewRows([]string{"address", "last_heartbeat", "query_count", "capacity", "is_active"}).
		AddRow("10.0.0.1:9000", "bad-time", int64(2), 1, true)

	mock.ExpectQuery(regexp.QuoteMeta("SELECT address, last_heartbeat, query_count, capacity, is_active")).
		WithArgs("task-a").
		WillReturnRows(rows)

	_, err := ps.GetServiceHistory(context.Background(), "task-a")
	if err == nil {
		t.Fatal("expected scan error, got nil")
	}
}

func TestGetServiceHistory_RowsErr(t *testing.T) {
	ps, mock, db := newMockStore(t)
	defer db.Close()

	now := time.Now().UTC()
	rows := sqlmock.NewRows([]string{"address", "last_heartbeat", "query_count", "capacity", "is_active"}).
		AddRow("10.0.0.1:9000", now, int64(2), 1, true).
		RowError(0, errors.New("row error"))

	mock.ExpectQuery(regexp.QuoteMeta("SELECT address, last_heartbeat, query_count, capacity, is_active")).
		WithArgs("task-a").
		WillReturnRows(rows)

	_, err := ps.GetServiceHistory(context.Background(), "task-a")
	if err == nil {
		t.Fatal("expected rows error, got nil")
	}
}

func TestNewPostgresStore_PingError(t *testing.T) {
	// Use TEST-NET-1 address and short connect timeout to force deterministic ping failure.
	_, err := NewPostgresStore("postgres://user:pass@192.0.2.1:5432/db?connect_timeout=1&sslmode=disable")
	if err == nil {
		t.Fatal("expected ping error, got nil")
	}
}

func TestNormalizedCapacity(t *testing.T) {
	cases := []struct {
		in   int
		want int
	}{
		{in: -10, want: 1},
		{in: 0, want: 1},
		{in: 1, want: 1},
		{in: 8, want: 8},
	}

	for _, tc := range cases {
		if got := normalizedCapacity(tc.in); got != tc.want {
			t.Fatalf("normalizedCapacity(%d) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

func TestSelectWeightedEntryIndex(t *testing.T) {
	entries := []store.ServiceEntry{
		{Address: "a", Capacity: 2},
		{Address: "b", Capacity: 1},
	}

	if got := totalStoreWeight(entries); got != 3 {
		t.Fatalf("expected total weight 3, got %d", got)
	}

	idx, ok := selectWeightedEntryIndex(entries, 0)
	if !ok || idx != 0 {
		t.Fatalf("expected slot 0 to select first entry, got idx=%d ok=%v", idx, ok)
	}
	idx, ok = selectWeightedEntryIndex(entries, 2)
	if !ok || idx != 1 {
		t.Fatalf("expected slot 2 to select second entry, got idx=%d ok=%v", idx, ok)
	}
	if _, ok := selectWeightedEntryIndex(entries, 3); ok {
		t.Fatalf("expected out-of-range slot selection to fail")
	}
}

func TestStoreServiceWeight(t *testing.T) {
	if got := storeServiceWeight(store.ServiceEntry{Capacity: 4}); got != 4 {
		t.Fatalf("expected weight 4, got %d", got)
	}
	if got := storeServiceWeight(store.ServiceEntry{Capacity: 0}); got != 1 {
		t.Fatalf("expected zero capacity to normalize to 1, got %d", got)
	}
}
