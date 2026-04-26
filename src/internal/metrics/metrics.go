package metrics

import (
	"context"
	"encoding/json"
	"runtime"
	"sync"
	"time"

	"gorm.io/gorm"

	"github.com/casapps/casgist/src/internal/database/models"
)

// Metrics represents application metrics
type Metrics struct {
	mu         sync.RWMutex
	db         *gorm.DB
	startTime  time.Time
	counters   map[string]int64
	gauges     map[string]float64
	histograms map[string]*Histogram
}

// Histogram represents a histogram metric
type Histogram struct {
	mu     sync.RWMutex
	values []float64
	sum    float64
	count  int64
}

// MetricsSnapshot represents a point-in-time view of metrics
type MetricsSnapshot struct {
	Timestamp      time.Time              `json:"timestamp"`
	Uptime         string                 `json:"uptime"`
	Version        string                 `json:"version"`
	GoVersion      string                 `json:"go_version"`
	Counters       map[string]int64       `json:"counters"`
	Gauges         map[string]float64     `json:"gauges"`
	Histograms     map[string]HistogramStats `json:"histograms"`
	System         SystemMetrics          `json:"system"`
	Database       DatabaseMetrics        `json:"database"`
	Application    ApplicationMetrics     `json:"application"`
}

// HistogramStats represents histogram statistics
type HistogramStats struct {
	Count   int64   `json:"count"`
	Sum     float64 `json:"sum"`
	Average float64 `json:"average"`
	Min     float64 `json:"min"`
	Max     float64 `json:"max"`
	P50     float64 `json:"p50"`
	P95     float64 `json:"p95"`
	P99     float64 `json:"p99"`
}

// SystemMetrics represents system-level metrics
type SystemMetrics struct {
	GoRoutines   int     `json:"goroutines"`
	MemoryUsed   uint64  `json:"memory_used"`
	MemoryTotal  uint64  `json:"memory_total"`
	CPUCount     int     `json:"cpu_count"`
	GCPauses     uint64  `json:"gc_pauses"`
	HeapObjects  uint64  `json:"heap_objects"`
	StackInUse   uint64  `json:"stack_in_use"`
}

// DatabaseMetrics represents database-related metrics
type DatabaseMetrics struct {
	Users         int64 `json:"users"`
	Gists         int64 `json:"gists"`
	Files         int64 `json:"files"`
	Stars         int64 `json:"stars"`
	Organizations int64 `json:"organizations"`
	Tags          int64 `json:"tags"`
}

// ApplicationMetrics represents application-specific metrics
type ApplicationMetrics struct {
	PublicGists  int64 `json:"public_gists"`
	PrivateGists int64 `json:"private_gists"`
	ActiveUsers  int64 `json:"active_users"`
	TotalViews   int64 `json:"total_views"`
}

// NewMetrics creates a new metrics instance
func NewMetrics(db *gorm.DB) *Metrics {
	return &Metrics{
		db:         db,
		startTime:  time.Now(),
		counters:   make(map[string]int64),
		gauges:     make(map[string]float64),
		histograms: make(map[string]*Histogram),
	}
}

// IncrementCounter increments a counter metric
func (m *Metrics) IncrementCounter(name string) {
	m.IncrementCounterBy(name, 1)
}

// IncrementCounterBy increments a counter metric by a specific value
func (m *Metrics) IncrementCounterBy(name string, value int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.counters[name] += value
}

// SetGauge sets a gauge metric value
func (m *Metrics) SetGauge(name string, value float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.gauges[name] = value
}

// RecordHistogram records a value in a histogram
func (m *Metrics) RecordHistogram(name string, value float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	
	hist, exists := m.histograms[name]
	if !exists {
		hist = &Histogram{
			values: make([]float64, 0, 1000), // Pre-allocate for performance
		}
		m.histograms[name] = hist
	}
	
	hist.mu.Lock()
	defer hist.mu.Unlock()
	
	hist.values = append(hist.values, value)
	hist.sum += value
	hist.count++
	
	// Keep only the last 1000 values to prevent memory growth
	if len(hist.values) > 1000 {
		hist.values = hist.values[len(hist.values)-1000:]
	}
}

// GetSnapshot returns a snapshot of current metrics
func (m *Metrics) GetSnapshot(version string) MetricsSnapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// Copy counters
	counters := make(map[string]int64)
	for k, v := range m.counters {
		counters[k] = v
	}

	// Copy gauges
	gauges := make(map[string]float64)
	for k, v := range m.gauges {
		gauges[k] = v
	}

	// Calculate histogram stats
	histograms := make(map[string]HistogramStats)
	for name, hist := range m.histograms {
		histograms[name] = hist.getStats()
	}

	return MetricsSnapshot{
		Timestamp:   time.Now(),
		Uptime:      time.Since(m.startTime).String(),
		Version:     version,
		GoVersion:   runtime.Version(),
		Counters:    counters,
		Gauges:      gauges,
		Histograms:  histograms,
		System:      m.getSystemMetrics(),
		Database:    m.getDatabaseMetrics(),
		Application: m.getApplicationMetrics(),
	}
}

// getStats calculates histogram statistics
func (h *Histogram) getStats() HistogramStats {
	h.mu.RLock()
	defer h.mu.RUnlock()

	if h.count == 0 {
		return HistogramStats{}
	}

	stats := HistogramStats{
		Count:   h.count,
		Sum:     h.sum,
		Average: h.sum / float64(h.count),
	}

	if len(h.values) > 0 {
		// Sort values for percentile calculation
		sorted := make([]float64, len(h.values))
		copy(sorted, h.values)
		
		// Simple insertion sort for small arrays
		for i := 1; i < len(sorted); i++ {
			key := sorted[i]
			j := i - 1
			for j >= 0 && sorted[j] > key {
				sorted[j+1] = sorted[j]
				j--
			}
			sorted[j+1] = key
		}

		stats.Min = sorted[0]
		stats.Max = sorted[len(sorted)-1]
		stats.P50 = percentile(sorted, 0.5)
		stats.P95 = percentile(sorted, 0.95)
		stats.P99 = percentile(sorted, 0.99)
	}

	return stats
}

// percentile calculates the percentile of a sorted slice
func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	
	index := p * float64(len(sorted)-1)
	lower := int(index)
	upper := lower + 1
	
	if upper >= len(sorted) {
		return sorted[len(sorted)-1]
	}
	
	weight := index - float64(lower)
	return sorted[lower]*(1-weight) + sorted[upper]*weight
}

// getSystemMetrics collects system-level metrics
func (m *Metrics) getSystemMetrics() SystemMetrics {
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)

	return SystemMetrics{
		GoRoutines:   runtime.NumGoroutine(),
		MemoryUsed:   mem.Alloc,
		MemoryTotal:  mem.TotalAlloc,
		CPUCount:     runtime.NumCPU(),
		GCPauses:     mem.PauseTotalNs,
		HeapObjects:  mem.HeapObjects,
		StackInUse:   mem.StackInuse,
	}
}

// getDatabaseMetrics collects database-related metrics
func (m *Metrics) getDatabaseMetrics() DatabaseMetrics {
	var metrics DatabaseMetrics

	if m.db != nil {
		// Count users
		m.db.Model(&models.User{}).Count(&metrics.Users)
		
		// Count gists
		m.db.Model(&models.Gist{}).Count(&metrics.Gists)
		
		// Count files
		m.db.Model(&models.GistFile{}).Count(&metrics.Files)
		
		// Count stars
		m.db.Model(&models.GistStar{}).Count(&metrics.Stars)
		
		// Count organizations
		m.db.Model(&models.Organization{}).Count(&metrics.Organizations)
		
		// Count tags
		m.db.Model(&models.Tag{}).Count(&metrics.Tags)
	}

	return metrics
}

// getApplicationMetrics collects application-specific metrics
func (m *Metrics) getApplicationMetrics() ApplicationMetrics {
	var metrics ApplicationMetrics

	if m.db != nil {
		// Count public/private gists
		m.db.Model(&models.Gist{}).Where("visibility = ?", models.VisibilityPublic).Count(&metrics.PublicGists)
		m.db.Model(&models.Gist{}).Where("visibility = ?", models.VisibilityPrivate).Count(&metrics.PrivateGists)
		
		// Count active users (logged in within last 30 days)
		thirtyDaysAgo := time.Now().AddDate(0, 0, -30)
		m.db.Model(&models.User{}).Where("last_login_at > ?", thirtyDaysAgo).Count(&metrics.ActiveUsers)
		
		// Count total views
		m.db.Model(&models.GistView{}).Count(&metrics.TotalViews)
	}

	return metrics
}

// ToJSON converts metrics snapshot to JSON
func (m *MetricsSnapshot) ToJSON() ([]byte, error) {
	return json.MarshalIndent(m, "", "  ")
}

// MetricsCollector collects metrics periodically
type MetricsCollector struct {
	metrics *Metrics
	ticker  *time.Ticker
	done    chan bool
}

// NewMetricsCollector creates a new metrics collector
func NewMetricsCollector(metrics *Metrics, interval time.Duration) *MetricsCollector {
	return &MetricsCollector{
		metrics: metrics,
		ticker:  time.NewTicker(interval),
		done:    make(chan bool),
	}
}

// Start begins collecting metrics
func (mc *MetricsCollector) Start(ctx context.Context) {
	go func() {
		for {
			select {
			case <-mc.ticker.C:
				mc.collectSystemMetrics()
			case <-mc.done:
				return
			case <-ctx.Done():
				return
			}
		}
	}()
}

// Stop stops collecting metrics
func (mc *MetricsCollector) Stop() {
	mc.ticker.Stop()
	close(mc.done)
}

// collectSystemMetrics collects system metrics periodically
func (mc *MetricsCollector) collectSystemMetrics() {
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)

	mc.metrics.SetGauge("system.goroutines", float64(runtime.NumGoroutine()))
	mc.metrics.SetGauge("system.memory.used", float64(mem.Alloc))
	mc.metrics.SetGauge("system.memory.heap_objects", float64(mem.HeapObjects))
	mc.metrics.SetGauge("system.gc.num", float64(mem.NumGC))
}

// RequestMetrics tracks HTTP request metrics
func (m *Metrics) RequestMetrics(method, path string, statusCode int, duration time.Duration) {
	// Increment request counter
	m.IncrementCounter("http.requests.total")
	m.IncrementCounter("http.requests." + method)
	m.IncrementCounter("http.requests.status." + string(rune(statusCode/100)) + "xx")

	// Record response time
	m.RecordHistogram("http.request.duration", duration.Seconds())
	m.RecordHistogram("http.request.duration."+method, duration.Seconds())
}

// AuthMetrics tracks authentication metrics
func (m *Metrics) AuthMetrics(event string, success bool) {
	m.IncrementCounter("auth.events.total")
	m.IncrementCounter("auth.events." + event)
	
	if success {
		m.IncrementCounter("auth.events." + event + ".success")
	} else {
		m.IncrementCounter("auth.events." + event + ".failure")
	}
}

// GistMetrics tracks gist-related metrics
func (m *Metrics) GistMetrics(event string) {
	m.IncrementCounter("gist.events.total")
	m.IncrementCounter("gist.events." + event)
}

// SearchMetrics tracks search metrics
func (m *Metrics) SearchMetrics(query string, resultCount int, duration time.Duration) {
	m.IncrementCounter("search.queries.total")
	m.RecordHistogram("search.duration", duration.Seconds())
	m.RecordHistogram("search.results", float64(resultCount))
}