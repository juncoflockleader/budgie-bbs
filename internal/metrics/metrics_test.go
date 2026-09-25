package metrics

import (
	"strings"
	"testing"
)

func TestCounter(t *testing.T) {
	r := NewRegistry()
	c := &Counter{n: "test_total", h: "A test counter."}
	r.register(c)
	c.Inc()
	c.Add(4)
	if c.Value() != 5 {
		t.Fatalf("expected 5, got %d", c.Value())
	}
	out := r.Gather()
	if !strings.Contains(out, "# TYPE test_total counter") {
		t.Errorf("missing TYPE line:\n%s", out)
	}
	if !strings.Contains(out, "test_total 5\n") {
		t.Errorf("missing value line:\n%s", out)
	}
}

func TestGauge(t *testing.T) {
	r := NewRegistry()
	g := &Gauge{n: "test_gauge", h: "A test gauge."}
	r.register(g)
	g.Inc()
	g.Inc()
	g.Dec()
	g.Set(10)
	if g.Value() != 10 {
		t.Fatalf("expected 10, got %d", g.Value())
	}
	out := r.Gather()
	if !strings.Contains(out, "# TYPE test_gauge gauge") {
		t.Errorf("missing TYPE line:\n%s", out)
	}
	if !strings.Contains(out, "test_gauge 10\n") {
		t.Errorf("missing value line:\n%s", out)
	}
}

func TestHistogram(t *testing.T) {
	r := NewRegistry()
	h := &Histogram{n: "test_ms", h: "A test histogram.", bounds: []float64{1, 10, 100}, counts: make([]uint64, 3)}
	r.register(h)
	h.Observe(0.5) // bucket 1, 10, 100
	h.Observe(5)   // bucket 10, 100
	h.Observe(50)  // bucket 100
	h.Observe(500) // +Inf only

	out := r.Gather()
	checks := []string{
		`# TYPE test_ms histogram`,
		`test_ms_bucket{le="1"} 1`,
		`test_ms_bucket{le="10"} 2`,
		`test_ms_bucket{le="100"} 3`,
		`test_ms_bucket{le="+Inf"} 4`,
		`test_ms_count 4`,
		`test_ms_sum 555.5`,
	}
	for _, c := range checks {
		if !strings.Contains(out, c) {
			t.Errorf("missing %q in:\n%s", c, out)
		}
	}
}

func TestCollectorWithLabels(t *testing.T) {
	r := NewRegistry()
	r.RegisterCollector(func() []Sample {
		return []Sample{
			{Name: "outbox_jobs", Help: "Outbox jobs by status.", Type: "gauge", Labels: map[string]string{"status": "pending"}, Value: 3},
			{Name: "outbox_jobs", Help: "Outbox jobs by status.", Type: "gauge", Labels: map[string]string{"status": "dead"}, Value: 1},
		}
	})
	out := r.Gather()
	if strings.Count(out, "# TYPE outbox_jobs gauge") != 1 {
		t.Errorf("expected one TYPE header for the family:\n%s", out)
	}
	if !strings.Contains(out, `outbox_jobs{status="pending"} 3`) {
		t.Errorf("missing pending sample:\n%s", out)
	}
	if !strings.Contains(out, `outbox_jobs{status="dead"} 1`) {
		t.Errorf("missing dead sample:\n%s", out)
	}
}

func TestEscapeLabelValue(t *testing.T) {
	got := formatLabels(map[string]string{"k": `a"b\c`})
	want := `{k="a\"b\\c"}`
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
