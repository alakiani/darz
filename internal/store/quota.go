package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// QuotaPeriod is one of a User's Quota Periods, from Start up to but not
// including End, both at 00:00 UTC (ADR 0010).
type QuotaPeriod struct {
	Start, End time.Time
}

// periodStart is where a User with this Renewal Day starts a period in the
// given month: on the Renewal Day, or on the month's last day if the month
// is too short for it.
func periodStart(renewalDay int, year int, month time.Month) time.Time {
	last := time.Date(year, month+1, 0, 0, 0, 0, 0, time.UTC).Day()
	return time.Date(year, month, min(renewalDay, last), 0, 0, 0, 0, time.UTC)
}

// QuotaPeriodAt returns the Quota Period that contains t for a User whose
// Renewal Day is renewalDay (1–31).
func QuotaPeriodAt(renewalDay int, t time.Time) QuotaPeriod {
	t = t.UTC()
	start := periodStart(renewalDay, t.Year(), t.Month())
	if t.Before(start) {
		start = periodStart(renewalDay, t.Year(), t.Month()-1)
	}
	return QuotaPeriod{Start: start, End: periodStart(renewalDay, start.Year(), start.Month()+1)}
}

// RenewalDay returns a User's Renewal Day: the UTC day of the month they
// registered.
func (s *Store) RenewalDay(ctx context.Context, userID string) (int, error) {
	var created int64
	err := s.db.QueryRowContext(ctx, `SELECT created_at FROM users WHERE id = ?`, userID).Scan(&created)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	return time.Unix(created, 0).UTC().Day(), err
}

// CurrentQuotaPeriod returns the User's Quota Period that contains now.
func (s *Store) CurrentQuotaPeriod(ctx context.Context, userID string) (QuotaPeriod, error) {
	rd, err := s.RenewalDay(ctx, userID)
	if err != nil {
		return QuotaPeriod{}, err
	}
	return QuotaPeriodAt(rd, s.Now()), nil
}

// PeriodUsage is the User's exact Usage, up plus down, in their current
// Quota Period, summed over all their Devices, revoked ones included.
func (s *Store) PeriodUsage(ctx context.Context, userID string) (uint64, error) {
	p, err := s.CurrentQuotaPeriod(ctx, userID)
	if err != nil {
		return 0, err
	}
	return periodBytes(ctx, s.db, userID, p)
}

// PeriodTotal is a User's Usage in one Quota Period, up plus down.
type PeriodTotal struct {
	QuotaPeriod
	Bytes uint64
}

// ClosePeriods stores one total per User for each Quota Period that has
// ended, while their daily rows still cover it exactly (90 days). It is
// safe to run any number of times: rerunning recomputes the stored totals,
// so late backlog still counts toward its period. Run it before RollUp.
func (s *Store) ClosePeriods(ctx context.Context) error {
	now := s.Now().UTC()
	exactFrom := now.Add(-DailyRetention).Truncate(24 * time.Hour)
	return s.tx(ctx, func(tx *sql.Tx) error {
		type user struct {
			id      string
			created time.Time
		}
		var users []user
		rows, err := tx.QueryContext(ctx, `SELECT id, created_at FROM users`)
		if err != nil {
			return err
		}
		for rows.Next() {
			var u user
			var created int64
			if err := rows.Scan(&u.id, &created); err != nil {
				rows.Close()
				return err
			}
			u.created = time.Unix(created, 0).UTC()
			users = append(users, u)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		for _, u := range users {
			rd := u.created.Day()
			p := QuotaPeriodAt(rd, now)
			for {
				p = QuotaPeriodAt(rd, p.Start.Add(-time.Second))
				if !p.End.After(u.created) || p.Start.Before(exactFrom) {
					break
				}
				n, err := periodBytes(ctx, tx, u.id, p)
				if err != nil {
					return err
				}
				_, err = tx.ExecContext(ctx, `INSERT INTO usage_periods (user_id, period_start, period_end, bytes) VALUES (?, ?, ?, ?)
					ON CONFLICT (user_id, period_start) DO UPDATE SET bytes = excluded.bytes`,
					u.id, p.Start.Unix(), p.End.Unix(), n)
				if err != nil {
					return err
				}
			}
		}
		return nil
	})
}

// ClosedPeriods returns the User's stored period totals, oldest first. It
// returns none for a User who doesn't exist.
func (s *Store) ClosedPeriods(ctx context.Context, userID string) ([]PeriodTotal, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT period_start, period_end, bytes FROM usage_periods
		WHERE user_id = ? ORDER BY period_start`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PeriodTotal{}
	for rows.Next() {
		var start, end int64
		var t PeriodTotal
		if err := rows.Scan(&start, &end, &t.Bytes); err != nil {
			return nil, err
		}
		t.Start, t.End = time.Unix(start, 0).UTC(), time.Unix(end, 0).UTC()
		out = append(out, t)
	}
	return out, rows.Err()
}

// DevicePeriodUsage is one live Device's Usage in a Quota Period.
type DevicePeriodUsage struct {
	ID, Name string
	Bytes    uint64
}

// PeriodUsageEntry is one Quota Period in a User's Usage history. Only
// periods that started within the last 90 days are Detailed: split into
// their live Devices with Usage, plus one total for all Revoked Devices.
type PeriodUsageEntry struct {
	PeriodTotal
	Current  bool
	Detailed bool
	Devices  []DevicePeriodUsage // nil unless Detailed
	Revoked  uint64
}

// UsageHistory returns a User's Usage by Quota Period, oldest first: the
// closed periods' stored totals, then the current period.
func (s *Store) UsageHistory(ctx context.Context, userID string) ([]PeriodUsageEntry, error) {
	cur, err := s.CurrentQuotaPeriod(ctx, userID)
	if err != nil {
		return nil, err
	}
	closed, err := s.ClosedPeriods(ctx, userID)
	if err != nil {
		return nil, err
	}
	n, err := periodBytes(ctx, s.db, userID, cur)
	if err != nil {
		return nil, err
	}
	exactFrom := s.Now().UTC().Add(-DailyRetention).Truncate(24 * time.Hour)
	out := make([]PeriodUsageEntry, 0, len(closed)+1)
	for _, t := range append(closed, PeriodTotal{cur, n}) {
		e := PeriodUsageEntry{PeriodTotal: t, Current: t.Start.Equal(cur.Start)}
		if !t.Start.Before(exactFrom) {
			if err := s.splitPeriod(ctx, userID, &e); err != nil {
				return nil, err
			}
		}
		out = append(out, e)
	}
	return out, nil
}

func (s *Store) splitPeriod(ctx context.Context, userID string, e *PeriodUsageEntry) error {
	rows, err := s.db.QueryContext(ctx, `SELECT d.id, d.name, d.revoked_at IS NOT NULL, sum(b) FROM (
			SELECT device_id, up + down AS b FROM usage_hourly WHERE hour >= ?2 AND hour < ?3
			UNION ALL SELECT device_id, up + down FROM usage_daily WHERE day >= ?2 AND day < ?3) u
		JOIN devices d ON d.id = u.device_id WHERE d.user_id = ?1
		GROUP BY d.id ORDER BY d.rowid`, userID, e.Start.Unix(), e.End.Unix())
	if err != nil {
		return err
	}
	defer rows.Close()
	e.Detailed, e.Devices = true, []DevicePeriodUsage{}
	for rows.Next() {
		var d DevicePeriodUsage
		var revoked bool
		if err := rows.Scan(&d.ID, &d.Name, &revoked, &d.Bytes); err != nil {
			return err
		}
		if revoked {
			e.Revoked += d.Bytes
		} else {
			e.Devices = append(e.Devices, d)
		}
	}
	return rows.Err()
}

// periodBytes sums a User's hourly and daily rows in a period. Periods
// start at 00:00 UTC and last at most 31 days, so the hourly (7 days) and
// daily (90 days) rows cover one exactly until it is 90 days old.
func periodBytes(ctx context.Context, q queryer, userID string, p QuotaPeriod) (uint64, error) {
	var n uint64
	err := q.QueryRowContext(ctx, `SELECT coalesce(sum(b), 0) FROM (
			SELECT up + down AS b FROM usage_hourly WHERE hour >= ?2 AND hour < ?3 AND device_id IN (SELECT id FROM devices WHERE user_id = ?1)
			UNION ALL SELECT up + down FROM usage_daily WHERE day >= ?2 AND day < ?3 AND device_id IN (SELECT id FROM devices WHERE user_id = ?1))`,
		userID, p.Start.Unix(), p.End.Unix()).Scan(&n)
	return n, err
}
