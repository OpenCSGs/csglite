package keygov

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/opencsgs/csglite/pkg/api"
	_ "modernc.org/sqlite"
)

// LedgerFile holds what each key, user and team used per day.
const LedgerFile = "spend.db"

const ledgerSchema = `
PRAGMA journal_mode=WAL;
PRAGMA busy_timeout=5000;
CREATE TABLE IF NOT EXISTS spend (
	subject TEXT NOT NULL,
	day TEXT NOT NULL,
	currency TEXT NOT NULL,
	requests INTEGER NOT NULL DEFAULT 0,
	tokens INTEGER NOT NULL DEFAULT 0,
	cost REAL NOT NULL DEFAULT 0,
	PRIMARY KEY (subject, day, currency)
);
`

const ledgerUpsert = `
INSERT INTO spend (subject, day, currency, requests, tokens, cost) VALUES (?, ?, ?, 1, ?, ?)
ON CONFLICT (subject, day, currency) DO UPDATE SET
	requests = spend.requests + 1,
	tokens = spend.tokens + excluded.tokens,
	cost = spend.cost + excluded.cost
`

// Subjects name who spending is counted against.
func KeySubject(id string) string  { return "key:" + id }
func UserSubject(id string) string { return "user:" + id }
func TeamSubject(id string) string { return "team:" + id }

// Spend is what a subject used since some time.
type Spend struct {
	Tokens int64
	// Cost is in the currency asked for; requests priced in another
	// currency, or not priced, add only tokens.
	Cost float64
}

// Ledger records spending per subject and local day. Days are local so that
// daily and monthly budgets restart at the operator's midnight.
type Ledger struct {
	mu   sync.Mutex
	path string
	db   *sql.DB
}

func NewLedger(appHome string) *Ledger {
	return &Ledger{path: filepath.Join(appHome, LedgerFile)}
}

func (l *Ledger) Close() error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.db == nil {
		return nil
	}
	err := l.db.Close()
	l.db = nil
	return err
}

// Record adds one request to every subject.
func (l *Ledger) Record(subjects []string, at time.Time, tokens int64, cost float64, currency string) error {
	if l == nil || len(subjects) == 0 {
		return nil
	}
	if cost <= 0 {
		cost, currency = 0, ""
	}
	day := at.Local().Format(time.DateOnly)
	l.mu.Lock()
	defer l.mu.Unlock()
	db, err := l.openLocked()
	if err != nil {
		return err
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	for _, subject := range subjects {
		if _, err := tx.Exec(ledgerUpsert, subject, day, strings.ToUpper(currency), tokens, cost); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("recording spend: %w", err)
		}
	}
	return tx.Commit()
}

// Spend sums what subject used from the local day of since onwards, with
// cost counted in currency. A zero since sums everything.
func (l *Ledger) Spend(subject string, since time.Time, currency string) (Spend, error) {
	if l == nil {
		return Spend{}, nil
	}
	from := ""
	if !since.IsZero() {
		from = since.Local().Format(time.DateOnly)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	db, err := l.openLocked()
	if err != nil {
		return Spend{}, err
	}
	var out Spend
	err = db.QueryRow(
		`SELECT COALESCE(SUM(tokens), 0), COALESCE(SUM(CASE WHEN currency = ? THEN cost ELSE 0 END), 0)
		FROM spend WHERE subject = ? AND day >= ?`,
		strings.ToUpper(currency), subject, from,
	).Scan(&out.Tokens, &out.Cost)
	if err != nil {
		return Spend{}, fmt.Errorf("reading spend: %w", err)
	}
	return out, nil
}

func (l *Ledger) openLocked() (*sql.DB, error) {
	if l.db != nil {
		return l.db, nil
	}
	db, err := sql.Open("sqlite", l.path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(ledgerSchema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("opening spend ledger: %w", err)
	}
	l.db = db
	return db, nil
}

// Current reads what subject spent against budget in the period containing
// now, and when that period began.
func (l *Ledger) Current(subject string, budget *api.Budget, now time.Time) (Spend, time.Time, error) {
	if budget == nil {
		return Spend{}, time.Time{}, nil
	}
	since := PeriodStart(budget.Period, now)
	spend, err := l.Spend(subject, since, budget.Currency)
	return spend, since, err
}
