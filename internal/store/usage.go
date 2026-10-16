package store

import (
	"context"
	"database/sql"
	"time"

	"github.com/alakiani/darz/internal/protocol"
)

// ADR 0003's retention.
const (
	HourlyRetention = 7 * 24 * time.Hour
	DailyRetention  = 90 * 24 * time.Hour
)

// PutUsage stores a Bridge's hourly buckets, replacing any it sent before
// for the same Device, Exit and hour. Buckets for addresses no Device holds
// are skipped. It returns how many it stored.
func (s *Store) PutUsage(ctx context.Context, nodeID string, buckets []protocol.UsageBucket) (int, error) {
	if len(buckets) == 0 {
		return 0, nil
	}
	stored := 0
	err := s.tx(ctx, func(tx *sql.Tx) error {
		ids := map[string]string{}
		rows, err := tx.Query(`SELECT address, id FROM devices`)
		if err != nil {
			return err
		}
		for rows.Next() {
			var addr, id string
			if err := rows.Scan(&addr, &id); err != nil {
				rows.Close()
				return err
			}
			ids[addr] = id
		}
		rows.Close()
		for _, b := range buckets {
			id, ok := ids[b.Device]
			if !ok || b.Hour%3600 != 0 {
				continue
			}
			_, err := tx.Exec(`INSERT INTO usage_hourly (node_id, device_id, exit_id, hour, up, down) VALUES (?, ?, ?, ?, ?, ?)
				ON CONFLICT (node_id, device_id, exit_id, hour) DO UPDATE SET up = excluded.up, down = excluded.down`,
				nodeID, id, b.Exit, b.Hour, b.Up, b.Down)
			if err != nil {
				return err
			}
			stored++
		}
		return nil
	})
	return stored, err
}

// RollUp folds hourly rows older than 7 days into daily totals, and daily
// rows older than 90 days into monthly totals per Device, deleting what it
// folded. It is safe to run any number of times.
func (s *Store) RollUp(ctx context.Context) error {
	now := s.Now().UTC()
	hourCut := now.Add(-HourlyRetention).Truncate(time.Hour).Unix()
	dayCut := now.Add(-DailyRetention).Truncate(24 * time.Hour).Unix()
	return s.tx(ctx, func(tx *sql.Tx) error {
		for _, q := range []string{
			`INSERT INTO usage_daily (device_id, exit_id, day, up, down)
				SELECT device_id, exit_id, hour - hour % 86400, sum(up), sum(down) FROM usage_hourly WHERE hour < ?1
				GROUP BY device_id, exit_id, hour - hour % 86400
				ON CONFLICT (device_id, exit_id, day) DO UPDATE SET up = up + excluded.up, down = down + excluded.down`,
			`DELETE FROM usage_hourly WHERE hour < ?1`,
		} {
			if _, err := tx.Exec(q, hourCut); err != nil {
				return err
			}
		}
		for _, q := range []string{
			`INSERT INTO usage_monthly (device_id, month, up, down)
				SELECT device_id, strftime('%Y-%m', day, 'unixepoch'), sum(up), sum(down) FROM usage_daily WHERE day < ?1
				GROUP BY device_id, strftime('%Y-%m', day, 'unixepoch')
				ON CONFLICT (device_id, month) DO UPDATE SET up = up + excluded.up, down = down + excluded.down`,
			`DELETE FROM usage_daily WHERE day < ?1`,
		} {
			if _, err := tx.Exec(q, dayCut); err != nil {
				return err
			}
		}
		return nil
	})
}

// UsageRow is bytes in one period, through one Exit (empty in monthly rows).
type UsageRow struct {
	Period string // RFC 3339 hour or day, or YYYY-MM
	Exit   string
	Up     uint64
	Down   uint64
}

// DeviceUsage is a Device's usage at each resolution: hourly for the last
// 7 days and daily for the last 90, per Exit; monthly in total.
type DeviceUsage struct {
	Hourly, Daily, Monthly []UsageRow
}

// Usage returns a Device's usage. Coarser views include what finer tables
// still hold, so nothing disappears while it waits to be rolled up.
func (s *Store) Usage(ctx context.Context, deviceID string) (DeviceUsage, error) {
	var u DeviceUsage
	var err error
	if u.Hourly, err = s.usageRows(ctx, `SELECT strftime('%Y-%m-%dT%H:00:00Z', hour, 'unixepoch'), exit_id, sum(up), sum(down)
		FROM usage_hourly WHERE device_id = ?1 GROUP BY hour, exit_id ORDER BY hour, exit_id`, deviceID); err != nil {
		return u, err
	}
	if u.Daily, err = s.usageRows(ctx, `SELECT strftime('%Y-%m-%d', d, 'unixepoch'), exit_id, sum(up), sum(down) FROM (
			SELECT day AS d, exit_id, up, down FROM usage_daily WHERE device_id = ?1
			UNION ALL SELECT hour - hour % 86400, exit_id, up, down FROM usage_hourly WHERE device_id = ?1)
		GROUP BY d, exit_id ORDER BY d, exit_id`, deviceID); err != nil {
		return u, err
	}
	u.Monthly, err = s.usageRows(ctx, `SELECT m, '', sum(up), sum(down) FROM (
			SELECT month AS m, up, down FROM usage_monthly WHERE device_id = ?1
			UNION ALL SELECT strftime('%Y-%m', day, 'unixepoch'), up, down FROM usage_daily WHERE device_id = ?1
			UNION ALL SELECT strftime('%Y-%m', hour, 'unixepoch'), up, down FROM usage_hourly WHERE device_id = ?1)
		GROUP BY m ORDER BY m`, deviceID)
	return u, err
}

func (s *Store) usageRows(ctx context.Context, q, deviceID string) ([]UsageRow, error) {
	rows, err := s.db.QueryContext(ctx, q, deviceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []UsageRow{}
	for rows.Next() {
		var r UsageRow
		if err := rows.Scan(&r.Period, &r.Exit, &r.Up, &r.Down); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// RecentBytes is how many bytes a Device moved in the current and the
// previous hour: what "used in the last hour" shows.
func (s *Store) RecentBytes(ctx context.Context, deviceID string) (uint64, error) {
	since := s.Now().UTC().Truncate(time.Hour).Add(-time.Hour).Unix()
	var n uint64
	err := s.db.QueryRowContext(ctx, `SELECT coalesce(sum(up + down), 0) FROM usage_hourly WHERE device_id = ? AND hour >= ?`,
		deviceID, since).Scan(&n)
	return n, err
}

// UserMonthBytes is a User's total across their Devices this calendar month
// (UTC): the per-User total Admins see.
func (s *Store) UserMonthBytes(ctx context.Context, userID string) (uint64, error) {
	now := s.Now().UTC()
	start := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC).Unix()
	month := now.Format("2006-01")
	var n uint64
	err := s.db.QueryRowContext(ctx, `SELECT coalesce(sum(b), 0) FROM (
			SELECT up + down AS b FROM usage_hourly WHERE hour >= ?2 AND device_id IN (SELECT id FROM devices WHERE user_id = ?1)
			UNION ALL SELECT up + down FROM usage_daily WHERE day >= ?2 AND device_id IN (SELECT id FROM devices WHERE user_id = ?1)
			UNION ALL SELECT up + down FROM usage_monthly WHERE month = ?3 AND device_id IN (SELECT id FROM devices WHERE user_id = ?1))`,
		userID, start, month).Scan(&n)
	return n, err
}
