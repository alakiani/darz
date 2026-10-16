package dataplane

import (
	"strings"
	"testing"

	"github.com/alakiani/darz/internal/metrics"
)

// Path flapping counts failure moves, not buckets: one Link dying moves
// many buckets in one step (ADR 0006, ADR 0008).
func TestMovesAreCountedByStepAndKind(t *testing.T) {
	p := &Plane{moves: map[[2]string]uint64{}, movedBuckets: map[[2]string]uint64{}}
	p.countMoves("Default", []move{{kind: moveFailure}, {kind: moveFailure}, {kind: moveFailure}})
	p.countMoves("Default", []move{{kind: moveRebalance}})
	p.countMoves("Default", nil)
	p.countMoves("IR", []move{{kind: moveFailure}})
	var w metrics.Writer
	p.WriteMetrics(&w)
	got := string(w.Bytes())
	for _, want := range []string{
		`darz_path_moves_total{rule="Default",kind="failure"} 1`,
		`darz_path_moves_total{rule="Default",kind="rebalance"} 1`,
		`darz_path_moves_total{rule="IR",kind="failure"} 1`,
		`darz_path_bucket_moves_total{rule="Default",kind="failure"} 3`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("metrics lack %q:\n%s", want, got)
		}
	}
}
