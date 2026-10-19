// Package metrics writes Prometheus's text exposition format, enough for
// the Agent's and the Coordinator's /metrics (ADR 0006) without pulling in
// a client library. No metric may carry a Device or User identifier.
package metrics

import (
	"bytes"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
)

// ContentType is the text exposition format's.
const ContentType = "text/plain; version=0.0.4; charset=utf-8"

// Writer builds one scrape's worth of metrics. Samples of one metric may
// be written in any order: each metric comes out once, its samples
// together under its HELP and TYPE, in the order metrics were first
// written. Raw text comes last.
type Writer struct {
	families map[string]*bytes.Buffer
	order    []string
	raw      bytes.Buffer
}

// Gauge writes one sample of a gauge. labels are name, value pairs.
func (w *Writer) Gauge(name, help string, v float64, labels ...string) {
	w.sample(name, "gauge", help, v, labels)
}

// Counter writes one sample of a counter.
func (w *Writer) Counter(name, help string, v float64, labels ...string) {
	w.sample(name, "counter", help, v, labels)
}

// Bool writes a gauge that is 1 for true and 0 for false.
func (w *Writer) Bool(name, help string, v bool, labels ...string) {
	f := 0.0
	if v {
		f = 1
	}
	w.Gauge(name, help, f, labels...)
}

// Raw appends text already in the exposition format, such as
// node_exporter's.
func (w *Writer) Raw(text []byte) {
	w.raw.Write(text)
	if len(text) > 0 && text[len(text)-1] != '\n' {
		w.raw.WriteByte('\n')
	}
}

// Bytes returns everything written.
func (w *Writer) Bytes() []byte {
	var out bytes.Buffer
	for _, name := range w.order {
		out.Write(w.families[name].Bytes())
	}
	out.Write(w.raw.Bytes())
	return out.Bytes()
}

// Serve answers a scrape with everything written.
func (w *Writer) Serve(rw http.ResponseWriter) {
	rw.Header().Set("Content-Type", ContentType)
	rw.Write(w.Bytes())
}

func (w *Writer) sample(name, kind, help string, v float64, labels []string) {
	if w.families == nil {
		w.families = map[string]*bytes.Buffer{}
	}
	b := w.families[name]
	if b == nil {
		b = &bytes.Buffer{}
		w.families[name] = b
		w.order = append(w.order, name)
		fmt.Fprintf(b, "# HELP %s %s\n# TYPE %s %s\n", name, help, name, kind)
	}
	b.WriteString(name)
	if len(labels) > 0 {
		b.WriteByte('{')
		for i := 0; i+1 < len(labels); i += 2 {
			if i > 0 {
				b.WriteByte(',')
			}
			fmt.Fprintf(b, "%s=\"%s\"", labels[i], escape(labels[i+1]))
		}
		b.WriteByte('}')
	}
	b.WriteByte(' ')
	b.WriteString(strconv.FormatFloat(v, 'g', -1, 64))
	b.WriteByte('\n')
}

// LinkLabels are the labels of a Link's metrics: its id, its two Nodes'
// names in order, which both ends agree on, and the far end's name, or
// "link <id>" until it is known.
func LinkLabels(id uint64, self, peer string) []string {
	if peer == "" {
		peer = fmt.Sprintf("link %d", id)
	}
	pair := []string{self, peer}
	slices.Sort(pair)
	return []string{"link_id", strconv.FormatUint(id, 10), "link", strings.Join(pair, "/"), "peer", peer}
}

var escaper = strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`)

func escape(s string) string { return escaper.Replace(s) }
