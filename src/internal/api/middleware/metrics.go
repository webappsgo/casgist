package middleware

import (
	"strconv"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/casapps/casgist/src/internal/metrics"
)

// MetricsMiddleware creates middleware for collecting HTTP metrics
func MetricsMiddleware(m *metrics.Metrics) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			start := time.Now()
			
			// Process request
			err := next(c)
			
			// Calculate duration
			duration := time.Since(start)
			
			// Get response status
			status := c.Response().Status
			if err != nil {
				if he, ok := err.(*echo.HTTPError); ok {
					status = he.Code
				} else {
					status = 500
				}
			}
			
			// Record metrics
			m.RequestMetrics(
				c.Request().Method,
				c.Path(),
				status,
				duration,
			)
			
			return err
		}
	}
}

// MetricsHandler provides metrics endpoint
func MetricsHandler(m *metrics.Metrics, version string) echo.HandlerFunc {
	return func(c echo.Context) error {
		snapshot := m.GetSnapshot(version)
		
		// Check format parameter
		format := c.QueryParam("format")
		if format == "json" || c.Request().Header.Get("Accept") == "application/json" {
			return c.JSON(200, snapshot)
		}
		
		// Return Prometheus format by default
		return c.String(200, formatPrometheus(snapshot))
	}
}

// formatPrometheus formats metrics in Prometheus exposition format
func formatPrometheus(snapshot metrics.MetricsSnapshot) string {
	var result string
	
	// Add metadata
	result += "# HELP casgists_info Application information\n"
	result += "# TYPE casgists_info gauge\n"
	result += "casgists_info{version=\"" + snapshot.Version + "\",go_version=\"" + snapshot.GoVersion + "\"} 1\n\n"
	
	// Add uptime
	result += "# HELP casgists_uptime_seconds Total uptime in seconds\n"
	result += "# TYPE casgists_uptime_seconds counter\n"
	result += "casgists_uptime_seconds " + strconv.FormatInt(int64(time.Since(time.Now()).Seconds()), 10) + "\n\n"
	
	// Add counters
	for name, value := range snapshot.Counters {
		metricName := "casgists_" + sanitizeMetricName(name)
		result += "# HELP " + metricName + " Counter metric\n"
		result += "# TYPE " + metricName + " counter\n"
		result += metricName + " " + strconv.FormatInt(value, 10) + "\n\n"
	}
	
	// Add gauges
	for name, value := range snapshot.Gauges {
		metricName := "casgists_" + sanitizeMetricName(name)
		result += "# HELP " + metricName + " Gauge metric\n"
		result += "# TYPE " + metricName + " gauge\n"
		result += metricName + " " + strconv.FormatFloat(value, 'f', -1, 64) + "\n\n"
	}
	
	// Add histogram summaries
	for name, hist := range snapshot.Histograms {
		baseName := "casgists_" + sanitizeMetricName(name)
		
		result += "# HELP " + baseName + " Histogram metric\n"
		result += "# TYPE " + baseName + " histogram\n"
		result += baseName + "_count " + strconv.FormatInt(hist.Count, 10) + "\n"
		result += baseName + "_sum " + strconv.FormatFloat(hist.Sum, 'f', -1, 64) + "\n"
		result += baseName + "_bucket{le=\"+Inf\"} " + strconv.FormatInt(hist.Count, 10) + "\n\n"
	}
	
	// Add system metrics
	result += "# HELP casgists_system_goroutines Number of goroutines\n"
	result += "# TYPE casgists_system_goroutines gauge\n"
	result += "casgists_system_goroutines " + strconv.Itoa(snapshot.System.GoRoutines) + "\n\n"
	
	result += "# HELP casgists_system_memory_used Memory used in bytes\n"
	result += "# TYPE casgists_system_memory_used gauge\n"
	result += "casgists_system_memory_used " + strconv.FormatUint(snapshot.System.MemoryUsed, 10) + "\n\n"
	
	// Add database metrics
	result += "# HELP casgists_database_users Total number of users\n"
	result += "# TYPE casgists_database_users gauge\n"
	result += "casgists_database_users " + strconv.FormatInt(snapshot.Database.Users, 10) + "\n\n"
	
	result += "# HELP casgists_database_gists Total number of gists\n"
	result += "# TYPE casgists_database_gists gauge\n"
	result += "casgists_database_gists " + strconv.FormatInt(snapshot.Database.Gists, 10) + "\n\n"
	
	return result
}

// sanitizeMetricName converts metric names to Prometheus format
func sanitizeMetricName(name string) string {
	// Replace dots and other characters with underscores
	result := ""
	for _, char := range name {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') {
			result += string(char)
		} else {
			result += "_"
		}
	}
	return result
}