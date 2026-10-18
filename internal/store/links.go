package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/alakiani/darz/internal/protocol"
)

// MeshSlots is how many /31s fit in the mesh range 10.78.0.0/16.
const MeshSlots = 1 << 15

// SetLinkPublicKey records a Node's Link key and reports whether it changed.
func (s *Store) SetLinkPublicKey(ctx context.Context, nodeID, key string) (bool, error) {
	res, err := s.db.ExecContext(ctx,
		`UPDATE nodes SET link_public_key = ? WHERE id = ? AND coalesce(link_public_key, '') != ?`, key, nodeID, key)
	if err != nil {
		return false, err
	}
	k, _ := res.RowsAffected()
	return k > 0, nil
}

// Link is a Link between two Nodes, NodeA's id sorting before NodeB's.
type Link struct {
	ID           uint64
	NodeA, NodeB string
	Disabled     bool
}

// EnsureLink returns the Link between two Nodes, creating it if needed.
func (s *Store) EnsureLink(ctx context.Context, a, b string) (Link, error) {
	if a > b {
		a, b = b, a
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO links (node_a, node_b) VALUES (?, ?) ON CONFLICT DO NOTHING`, a, b); err != nil {
		return Link{}, err
	}
	l := Link{NodeA: a, NodeB: b}
	err := s.db.QueryRowContext(ctx, `SELECT id, disabled FROM links WHERE node_a = ? AND node_b = ?`, a, b).Scan(&l.ID, &l.Disabled)
	return l, err
}

// Links returns every Link.
func (s *Store) Links(ctx context.Context) ([]Link, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, node_a, node_b, disabled FROM links ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var links []Link
	for rows.Next() {
		var l Link
		if err := rows.Scan(&l.ID, &l.NodeA, &l.NodeB, &l.Disabled); err != nil {
			return nil, err
		}
		links = append(links, l)
	}
	return links, rows.Err()
}

// Instance is one live candidate Transport instance of a Link.
type Instance struct {
	ID         uint64
	LinkID     uint64
	Transport  protocol.Transport
	Listener   string // the Node that listens and chose the port and parameters
	Slot       int
	ListenPort uint16              // 0 until the listener reports it
	AWG        *protocol.AWGParams // nil until the listener reports it
	CreatedAt  time.Time
	DownSince  *time.Time
}

// Instances returns every live instance, oldest first.
func (s *Store) Instances(ctx context.Context) ([]Instance, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, link_id, transport, listener, slot, coalesce(listen_port, 0),
		coalesce(awg_params, ''), created_at, down_since FROM link_instances WHERE retired_at IS NULL ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Instance
	for rows.Next() {
		var in Instance
		var awg string
		var created int64
		var down sql.NullInt64
		if err := rows.Scan(&in.ID, &in.LinkID, &in.Transport, &in.Listener, &in.Slot, &in.ListenPort, &awg, &created, &down); err != nil {
			return nil, err
		}
		in.CreatedAt = time.Unix(created, 0).UTC()
		if down.Valid {
			t := time.Unix(down.Int64, 0).UTC()
			in.DownSince = &t
		}
		if awg != "" {
			in.AWG = new(protocol.AWGParams)
			if err := json.Unmarshal([]byte(awg), in.AWG); err != nil {
				return nil, err
			}
		}
		out = append(out, in)
	}
	return out, rows.Err()
}

// ErrMeshFull means every /31 in the mesh range is taken.
var ErrMeshFull = errors.New("mesh range exhausted")

// CreateInstance adds an instance to a Link, on the lowest free /31.
func (s *Store) CreateInstance(ctx context.Context, linkID uint64, transport protocol.Transport, listener string) (Instance, error) {
	in := Instance{LinkID: linkID, Transport: transport, Listener: listener, CreatedAt: time.Unix(s.unix(), 0).UTC()}
	err := s.tx(ctx, func(tx *sql.Tx) error {
		// The lowest slot not held by a live instance.
		err := tx.QueryRow(`SELECT coalesce(min(s.slot + 1), 0) FROM (SELECT -1 AS slot UNION SELECT slot FROM link_instances WHERE retired_at IS NULL) s
			WHERE s.slot + 1 NOT IN (SELECT slot FROM link_instances WHERE retired_at IS NULL)`).Scan(&in.Slot)
		if err != nil {
			return err
		}
		if in.Slot >= MeshSlots {
			return ErrMeshFull
		}
		res, err := tx.Exec(`INSERT INTO link_instances (link_id, transport, listener, slot, created_at) VALUES (?, ?, ?, ?, ?)`,
			linkID, transport, listener, in.Slot, in.CreatedAt.Unix())
		if err != nil {
			return err
		}
		id, err := res.LastInsertId()
		in.ID = uint64(id)
		return err
	})
	return in, err
}

// RetireInstance takes an instance out of service for good.
func (s *Store) RetireInstance(ctx context.Context, id uint64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE link_instances SET retired_at = ? WHERE id = ? AND retired_at IS NULL`, s.unix(), id)
	return err
}

// SetListenChoice records the port and parameters the listener chose. It
// only takes the first choice, since an instance's parameters never change,
// and only from the instance's listener. It reports whether it stored
// anything.
func (s *Store) SetListenChoice(ctx context.Context, id uint64, listener string, port uint16, awg *protocol.AWGParams) (bool, error) {
	var params any
	if awg != nil {
		b, err := json.Marshal(awg)
		if err != nil {
			return false, err
		}
		params = string(b)
	}
	res, err := s.db.ExecContext(ctx, `UPDATE link_instances SET listen_port = ?, awg_params = ?
		WHERE id = ? AND listener = ? AND listen_port IS NULL AND retired_at IS NULL`, port, params, id, listener)
	if err != nil {
		return false, err
	}
	k, _ := res.RowsAffected()
	return k > 0, nil
}

// SetInstanceDownSince records when the initiating end last saw the
// instance stop being Up, or clears it.
func (s *Store) SetInstanceDownSince(ctx context.Context, id uint64, since *time.Time) error {
	var v any
	if since != nil {
		v = since.Unix()
	}
	_, err := s.db.ExecContext(ctx, `UPDATE link_instances SET down_since = ? WHERE id = ? AND retired_at IS NULL`, v, id)
	return err
}

// Transports returns the global ordered candidate Transport list. Until an
// Admin changes it, every Transport is on, GRE included (ADR 0009).
func (s *Store) Transports(ctx context.Context) ([]protocol.Transport, error) {
	var v string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = 'transports'`).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return []protocol.Transport{protocol.TransportWireGuard, protocol.TransportAmneziaWG, protocol.TransportGRE}, nil
	}
	if err != nil {
		return nil, err
	}
	var ts []protocol.Transport
	if err := json.Unmarshal([]byte(v), &ts); err != nil {
		return nil, fmt.Errorf("settings.transports: %w", err)
	}
	return ts, nil
}

// SetTransports replaces the global candidate Transport list.
func (s *Store) SetTransports(ctx context.Context, ts []protocol.Transport) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO settings (key, value) VALUES ('transports', ?)
		ON CONFLICT (key) DO UPDATE SET value = excluded.value`, mustJSON(nonNil(ts)))
	return err
}
