package store

import "context"

// EndpointSlot is an Endpoint Slot's assignment as stored (ADR 0005).
type EndpointSlot struct {
	Name     string
	NodeID   string // the Bridge it is assigned to
	Addr     string // what its DNS record was last set to; "" while an update is due
	Changed  int64  // Unix time NodeID last changed
	Repoints int    // times it moved off a Bridge
}

// EndpointSlots returns every assigned slot by name.
func (s *Store) EndpointSlots(ctx context.Context) ([]EndpointSlot, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT name, node_id, addr, changed_at, repoints FROM endpoint_slots ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []EndpointSlot
	for rows.Next() {
		var e EndpointSlot
		if err := rows.Scan(&e.Name, &e.NodeID, &e.Addr, &e.Changed, &e.Repoints); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// AssignSlot points a slot at a Bridge. A move clears the slot's address,
// so its DNS record is updated next, and counts as a re-point.
func (s *Store) AssignSlot(ctx context.Context, name, nodeID string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO endpoint_slots (name, node_id, changed_at) VALUES (?, ?, ?)
		ON CONFLICT (name) DO UPDATE SET node_id = excluded.node_id, addr = '', changed_at = excluded.changed_at,
			repoints = repoints + 1
		WHERE node_id != excluded.node_id`, name, nodeID, s.unix())
	return err
}

// SetSlotAddr records that a slot's DNS record now holds addr.
func (s *Store) SetSlotAddr(ctx context.Context, name, addr string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE endpoint_slots SET addr = ? WHERE name = ?`, addr, name)
	return err
}
