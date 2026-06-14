// Package metrics is a tiny, zero-dependency in-process metrics registry that
// renders Prometheus text exposition format. It is deliberately minimal: just
// the counter, gauge, and histogram primitives BudgieBBS needs to observe its
// cluster behavior (W7 of the scaling milestone).
//
// Hot-path primitives (Counter, Gauge) are lock-free atomics. Histograms use a
// small mutex; they sit on lower-frequency paths (per-command, per-replay).
//
// Scrape-time values that must be computed on demand — open SSH sessions,
// outbox job counts — are exposed via RegisterCollector.
package metrics

import (
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

// Default is the process-wide registry. The package-level metric variables in
// vars.go register themselves here.
var Default = NewRegistry()

// Registry holds named metric families and ad-hoc collectors.
type Registry struct {
	mu         sync.Mutex
	metrics    []metric          // registered in declaration order
	collectors []func() []Sample // computed at scrape time
}

func NewRegistry() *Registry { return &Registry{} }

// metric is the internal interface every registered primitive implements.
type metric interface {
	name() string
	help() string
	typ() string
	write(sb *strings.Builder)
}

// Sample is a single labeled value emitted by a collector.
type Sample struct {
	Name   string
	Help   string
	Type   string // "gauge" or "counter"
	Labels map[string]string
	Value  float64
}

func (r *Registry) register(m metric) {
	r.mu.Lock()
	r.metrics = append(r.metrics, m)
	r.mu.Unlock()
}

// RegisterCollector adds a function invoked on every scrape. Use it for values
// that cannot be tracked incrementally (e.g. a COUNT(*) over a table, or the
// length of an in-memory registry). Samples sharing a Name are grouped under a
// single HELP/TYPE header.
func (r *Registry) RegisterCollector(fn func() []Sample) {
	r.mu.Lock()
	r.collectors = append(r.collectors, fn)
	r.mu.Unlock()
}

// RegisterCollector adds a collector to the default registry.
func RegisterCollector(fn func() []Sample) { Default.RegisterCollector(fn) }

// Gather renders the full registry in Prometheus text exposition format.
func (r *Registry) Gather() string {
	r.mu.Lock()
	metrics := append([]metric(nil), r.metrics...)
	collectors := append([]func() []Sample(nil), r.collectors...)
	r.mu.Unlock()

	var sb strings.Builder
	for _, m := range metrics {
		sb.WriteString("# HELP ")
		sb.WriteString(m.name())
		sb.WriteByte(' ')
		sb.WriteString(m.help())
		sb.WriteByte('\n')
		sb.WriteString("# TYPE ")
		sb.WriteString(m.name())
		sb.WriteByte(' ')
		sb.WriteString(m.typ())
		sb.WriteByte('\n')
		m.write(&sb)
	}

	// Group collector samples by metric name so each family emits one header.
	var samples []Sample
	for _, fn := range collectors {
		samples = append(samples, fn()...)
	}
	writeSamples(&sb, samples)
	return sb.String()
}

// Gather renders the default registry.
func Gather() string { return Default.Gather() }

func writeSamples(sb *strings.Builder, samples []Sample) {
	// Stable grouping by name, preserving first-seen order.
	order := []string{}
	byName := map[string][]Sample{}
	meta := map[string]Sample{}
	for _, s := range samples {
		if _, ok := byName[s.Name]; !ok {
			order = append(order, s.Name)
			meta[s.Name] = s
		}
		byName[s.Name] = append(byName[s.Name], s)
	}
	for _, name := range order {
		m := meta[name]
		sb.WriteString("# HELP ")
		sb.WriteString(name)
		sb.WriteByte(' ')
		sb.WriteString(m.Help)
		sb.WriteByte('\n')
		sb.WriteString("# TYPE ")
		sb.WriteString(name)
		sb.WriteByte(' ')
		if m.Type == "" {
			sb.WriteString("gauge")
		} else {
			sb.WriteString(m.Type)
		}
		sb.WriteByte('\n')
		for _, s := range byName[name] {
			sb.WriteString(name)
			sb.WriteString(formatLabels(s.Labels))
			sb.WriteByte(' ')
			sb.WriteString(formatFloat(s.Value))
			sb.WriteByte('\n')
		}
	}
}

// --- Counter ---

// Counter is a monotonically increasing value.
type Counter struct {
	n, h string
	v    atomic.Int64
}

// NewCounter registers a counter on the default registry.
func NewCounter(name, help string) *Counter {
	c := &Counter{n: name, h: help}
	Default.register(c)
	return c
}

func (c *Counter) Inc()            { c.v.Add(1) }
func (c *Counter) Add(delta int64) { c.v.Add(delta) }
func (c *Counter) Value() int64    { return c.v.Load() }

func (c *Counter) name() string { return c.n }
func (c *Counter) help() string { return c.h }
func (c *Counter) typ() string  { return "counter" }
func (c *Counter) write(sb *strings.Builder) {
	sb.WriteString(c.n)
	sb.WriteByte(' ')
	sb.WriteString(strconv.FormatInt(c.v.Load(), 10))
	sb.WriteByte('\n')
}

// --- CounterVec ---

// CounterVec is a monotonically increasing counter split by a fixed label set.
// It is intentionally small and best suited to low-cardinality operational
// dimensions such as transport or event scope.
type CounterVec struct {
	n, h       string
	labelNames []string

	mu     sync.Mutex
	values map[string]counterVecSample
}

type counterVecSample struct {
	labels map[string]string
	value  int64
}

// NewCounterVec registers a labeled counter on the default registry.
func NewCounterVec(name, help string, labelNames []string) *CounterVec {
	cv := &CounterVec{
		n:          name,
		h:          help,
		labelNames: append([]string(nil), labelNames...),
		values:     map[string]counterVecSample{},
	}
	Default.register(cv)
	return cv
}

func (cv *CounterVec) Inc(labels map[string]string) { cv.Add(labels, 1) }

func (cv *CounterVec) Add(labels map[string]string, delta int64) {
	key, normalized := cv.normalize(labels)
	cv.mu.Lock()
	sample := cv.values[key]
	if sample.labels == nil {
		sample.labels = normalized
	}
	sample.value += delta
	cv.values[key] = sample
	cv.mu.Unlock()
}

func (cv *CounterVec) Value(labels map[string]string) int64 {
	key, _ := cv.normalize(labels)
	cv.mu.Lock()
	defer cv.mu.Unlock()
	return cv.values[key].value
}

func (cv *CounterVec) Samples() []Sample {
	cv.mu.Lock()
	keys := make([]string, 0, len(cv.values))
	for key := range cv.values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	samples := make([]Sample, 0, len(keys))
	for _, key := range keys {
		value := cv.values[key]
		samples = append(samples, Sample{
			Name:   cv.n,
			Help:   cv.h,
			Type:   cv.typ(),
			Labels: copyLabels(value.labels),
			Value:  float64(value.value),
		})
	}
	cv.mu.Unlock()
	return samples
}

func (cv *CounterVec) name() string { return cv.n }
func (cv *CounterVec) help() string { return cv.h }
func (cv *CounterVec) typ() string  { return "counter" }
func (cv *CounterVec) write(sb *strings.Builder) {
	cv.mu.Lock()
	keys := make([]string, 0, len(cv.values))
	for key := range cv.values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	samples := make([]counterVecSample, 0, len(keys))
	for _, key := range keys {
		samples = append(samples, cv.values[key])
	}
	cv.mu.Unlock()

	for _, sample := range samples {
		sb.WriteString(cv.n)
		sb.WriteString(formatLabels(sample.labels))
		sb.WriteByte(' ')
		sb.WriteString(strconv.FormatInt(sample.value, 10))
		sb.WriteByte('\n')
	}
}

func (cv *CounterVec) normalize(labels map[string]string) (string, map[string]string) {
	normalized := make(map[string]string, len(cv.labelNames))
	parts := make([]string, len(cv.labelNames))
	for i, name := range cv.labelNames {
		value := labels[name]
		normalized[name] = value
		parts[i] = name + "\x00" + value
	}
	return strings.Join(parts, "\x00"), normalized
}

func copyLabels(labels map[string]string) map[string]string {
	if labels == nil {
		return nil
	}
	out := make(map[string]string, len(labels))
	for name, value := range labels {
		out[name] = value
	}
	return out
}

// --- Gauge ---

// Gauge is a value that can go up or down.
type Gauge struct {
	n, h string
	v    atomic.Int64
}

// NewGauge registers a gauge on the default registry.
func NewGauge(name, help string) *Gauge {
	g := &Gauge{n: name, h: help}
	Default.register(g)
	return g
}

func (g *Gauge) Inc()         { g.v.Add(1) }
func (g *Gauge) Dec()         { g.v.Add(-1) }
func (g *Gauge) Add(d int64)  { g.v.Add(d) }
func (g *Gauge) Set(v int64)  { g.v.Store(v) }
func (g *Gauge) Value() int64 { return g.v.Load() }

func (g *Gauge) name() string { return g.n }
func (g *Gauge) help() string { return g.h }
func (g *Gauge) typ() string  { return "gauge" }
func (g *Gauge) write(sb *strings.Builder) {
	sb.WriteString(g.n)
	sb.WriteByte(' ')
	sb.WriteString(strconv.FormatInt(g.v.Load(), 10))
	sb.WriteByte('\n')
}

// --- Histogram ---

// Histogram tracks a distribution with cumulative buckets, plus sum and count,
// rendered as a Prometheus histogram. Bucket bounds are upper-inclusive (le).
type Histogram struct {
	n, h   string
	bounds []float64

	mu     sync.Mutex
	counts []uint64
	sum    float64
	total  uint64
}

// NewHistogram registers a histogram. bounds must be sorted ascending.
func NewHistogram(name, help string, bounds []float64) *Histogram {
	cp := append([]float64(nil), bounds...)
	sort.Float64s(cp)
	hi := &Histogram{n: name, h: help, bounds: cp, counts: make([]uint64, len(cp))}
	Default.register(hi)
	return hi
}

// Observe records a single value.
func (hi *Histogram) Observe(v float64) {
	hi.mu.Lock()
	hi.sum += v
	hi.total++
	for i, b := range hi.bounds {
		if v <= b {
			hi.counts[i]++
		}
	}
	hi.mu.Unlock()
}

func (hi *Histogram) name() string { return hi.n }
func (hi *Histogram) help() string { return hi.h }
func (hi *Histogram) typ() string  { return "histogram" }
func (hi *Histogram) write(sb *strings.Builder) {
	hi.mu.Lock()
	defer hi.mu.Unlock()
	for i, b := range hi.bounds {
		sb.WriteString(hi.n)
		sb.WriteString(`_bucket{le="`)
		sb.WriteString(formatFloat(b))
		sb.WriteString(`"} `)
		sb.WriteString(strconv.FormatUint(hi.counts[i], 10))
		sb.WriteByte('\n')
	}
	sb.WriteString(hi.n)
	sb.WriteString(`_bucket{le="+Inf"} `)
	sb.WriteString(strconv.FormatUint(hi.total, 10))
	sb.WriteByte('\n')
	sb.WriteString(hi.n)
	sb.WriteString("_sum ")
	sb.WriteString(formatFloat(hi.sum))
	sb.WriteByte('\n')
	sb.WriteString(hi.n)
	sb.WriteString("_count ")
	sb.WriteString(strconv.FormatUint(hi.total, 10))
	sb.WriteByte('\n')
}

// --- helpers ---

func formatFloat(f float64) string {
	return strconv.FormatFloat(f, 'g', -1, 64)
}

func formatLabels(labels map[string]string) string {
	if len(labels) == 0 {
		return ""
	}
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var sb strings.Builder
	sb.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			sb.WriteByte(',')
		}
		sb.WriteString(k)
		sb.WriteString(`="`)
		sb.WriteString(escapeLabelValue(labels[k]))
		sb.WriteByte('"')
	}
	sb.WriteByte('}')
	return sb.String()
}

func escapeLabelValue(v string) string {
	v = strings.ReplaceAll(v, `\`, `\\`)
	v = strings.ReplaceAll(v, `"`, `\"`)
	v = strings.ReplaceAll(v, "\n", `\n`)
	return v
}
