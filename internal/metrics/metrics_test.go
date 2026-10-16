package metrics

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWriter(t *testing.T) {
	var w Writer
	w.Gauge("darz_link_up", "1 while the Link has a working transport.", 1, "link", "ab1/ir1", "peer", "ab1")
	w.Counter("darz_moves_total", "Moves.", 3)
	w.Gauge("darz_link_up", "ignored: help is written once", 0, "link", "ir1/ir3", "peer", "ir3")
	w.Gauge("darz_odd", "Label values are escaped.", 0.5, "v", "a\"b\\c\nd")
	got := string(w.Bytes())
	want := `# HELP darz_link_up 1 while the Link has a working transport.
# TYPE darz_link_up gauge
darz_link_up{link="ab1/ir1",peer="ab1"} 1
darz_link_up{link="ir1/ir3",peer="ir3"} 0
# HELP darz_moves_total Moves.
# TYPE darz_moves_total counter
darz_moves_total 3
# HELP darz_odd Label values are escaped.
# TYPE darz_odd gauge
darz_odd{v="a\"b\\c\nd"} 0.5
`
	if got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}

func TestRawIsAppended(t *testing.T) {
	var w Writer
	w.Gauge("a", "A.", 1)
	w.Raw([]byte("node_load1 0.5"))
	if got := string(w.Bytes()); !strings.HasSuffix(got, "a 1\nnode_load1 0.5\n") {
		t.Errorf("got %q", got)
	}
}

func TestServe(t *testing.T) {
	rec := httptest.NewRecorder()
	var w Writer
	w.Gauge("a", "A.", 1)
	w.Serve(rec)
	if ct := rec.Header().Get("Content-Type"); ct != ContentType {
		t.Errorf("Content-Type %q", ct)
	}
	if rec.Body.String() != "# HELP a A.\n# TYPE a gauge\na 1\n" {
		t.Errorf("body %q", rec.Body.String())
	}
}
