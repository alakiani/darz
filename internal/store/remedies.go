package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// ErrUnlimited means the User has an unlimited Quota, so a Reset, or a
// Refill redeemed, has nothing to bring back (ADR 0010).
var ErrUnlimited = errors.New("unlimited quota")

// Remedy is a Top-up or a Reset as recorded in one of a User's Quota
// Periods.
type Remedy struct {
	PeriodStart time.Time
	Kind        RemedyKind
	Bytes       uint64
	At          time.Time
}

// Remedies returns every Top-up and Reset of a User, oldest first.
func (s *Store) Remedies(ctx context.Context, userID string) ([]Remedy, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT period_start, kind, bytes, created_at FROM quota_remedies
		WHERE user_id = ? ORDER BY id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Remedy{}
	for rows.Next() {
		var r Remedy
		var start, at int64
		if err := rows.Scan(&start, &r.Kind, &r.Bytes, &at); err != nil {
			return nil, err
		}
		r.PeriodStart, r.At = time.Unix(start, 0).UTC(), time.Unix(at, 0).UTC()
		out = append(out, r)
	}
	return out, rows.Err()
}

// ResetQuota records a Reset in the User's current Quota Period and
// returns what it added: the Quota minus what they may still use, fixed
// now, so Usage that arrives later never changes it.
func (s *Store) ResetQuota(ctx context.Context, userID string) (uint64, error) {
	var n uint64
	err := s.tx(ctx, func(tx *sql.Tx) error {
		var err error
		n, err = s.reset(ctx, tx, userID)
		return err
	})
	return n, err
}

func (s *Store) reset(ctx context.Context, tx *sql.Tx, userID string) (uint64, error) {
	q, err := s.quotaStanding(ctx, tx, userID)
	if err != nil {
		return 0, err
	}
	n, limited := q.ResetAdds()
	if !limited {
		return 0, ErrUnlimited
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO quota_remedies (user_id, period_start, kind, bytes, created_at) VALUES (?, ?, ?, ?, ?)`,
		userID, q.Period.Start.Unix(), string(Reset), n, s.unix())
	return n, err
}

// Refill is a Reset a User holds, redeemable up to and including ExpiresAt.
type Refill struct {
	ID        string
	ExpiresAt time.Time
}

// GiveRefills gives a User count Refills, each lasting ttl from now.
func (s *Store) GiveRefills(ctx context.Context, userID string, count int, ttl time.Duration) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM users WHERE id = ?`, userID).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			return ErrNotFound
		}
		return s.giveRefills(ctx, tx, userID, count, ttl)
	})
}

func (s *Store) giveRefills(ctx context.Context, tx *sql.Tx, userID string, count int, ttl time.Duration) error {
	expires := s.unix() + int64(ttl/time.Second)
	for range count {
		if _, err := tx.ExecContext(ctx, `INSERT INTO refills (id, user_id, created_at, expires_at) VALUES (?, ?, ?, ?)`,
			"rf_"+randomHex(8), userID, s.unix(), expires); err != nil {
			return err
		}
	}
	return nil
}

// HeldRefills returns the Refills a User holds, neither redeemed nor
// expired, soonest expiry first.
func (s *Store) HeldRefills(ctx context.Context, userID string) ([]Refill, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, expires_at FROM refills
		WHERE user_id = ? AND redeemed_at IS NULL AND expires_at >= ? ORDER BY expires_at, rowid`, userID, s.unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Refill{}
	for rows.Next() {
		var r Refill
		var expires int64
		if err := rows.Scan(&r.ID, &expires); err != nil {
			return nil, err
		}
		r.ExpiresAt = time.Unix(expires, 0).UTC()
		out = append(out, r)
	}
	return out, rows.Err()
}

// HeldRefillCount is how many Refills a User holds.
func (s *Store) HeldRefillCount(ctx context.Context, userID string) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM refills WHERE user_id = ? AND redeemed_at IS NULL AND expires_at >= ?`,
		userID, s.unix()).Scan(&n)
	return n, err
}

// RedeemRefill turns one of a User's held Refills into a Reset of their
// current Quota Period and returns what it added. A Refill that is not
// theirs, already redeemed or expired is ErrNotFound; an unlimited Quota
// is ErrUnlimited and leaves the Refill held.
func (s *Store) RedeemRefill(ctx context.Context, userID, refillID string) (uint64, error) {
	var n uint64
	err := s.tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE refills SET redeemed_at = ?1
			WHERE id = ?2 AND user_id = ?3 AND redeemed_at IS NULL AND expires_at >= ?1`, s.unix(), refillID, userID)
		if err != nil {
			return err
		}
		if k, _ := res.RowsAffected(); k == 0 {
			return ErrNotFound
		}
		n, err = s.reset(ctx, tx, userID)
		return err
	})
	return n, err
}
