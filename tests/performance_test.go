//go:build integration
// +build integration

package tests

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"

	testingSuite "github.com/casapps/casgist/src/internal/testing"
)

// PerformanceTestSuite tests performance and load handling
type PerformanceTestSuite struct {
	testingSuite.TestSuite
}

// TestPerformanceTestSuite runs the performance test suite
func TestPerformanceTestSuite(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping performance tests in short mode")
	}
	suite.Run(t, new(PerformanceTestSuite))
}

// BenchmarkHealthCheck benchmarks the health check endpoint
func (s *PerformanceTestSuite) TestBenchmarkHealthCheck() {
	s.T().Run("HealthCheck", func(t *testing.T) {
		b := &testing.B{}
		s.BenchmarkAPI(b, "GET", "/health", nil)

		t.Logf("Health check benchmark completed")
	})
}

// BenchmarkGistList benchmarks gist listing
func (s *PerformanceTestSuite) TestBenchmarkGistList() {
	// Create test data
	user := s.TestData.CreateTestUserWithData(s.T(), map[string]interface{}{
		"username": "perfuser",
		"password": "TestPassword123!",
	})

	// Create multiple gists for testing
	for i := 0; i < 10; i++ {
		s.TestData.CreateTestGist(s.T(), user, map[string]interface{}{
			"title":      "Performance Test Gist",
			"visibility": "public",
		})
	}

	s.T().Run("GistList", func(t *testing.T) {
		b := &testing.B{}
		s.BenchmarkAPI(b, "GET", "/api/v1/gists", nil)

		t.Logf("Gist list benchmark completed")
	})
}

// TestLoadTestHealthCheck performs load testing on health check
func (s *PerformanceTestSuite) TestLoadTestHealthCheck() {
	duration := 10 * time.Second
	concurrency := 10

	result := s.LoadTest("/health", duration, concurrency)

	s.T().Logf("Load test results:")
	s.T().Logf("Duration: %v", result.Duration)
	s.T().Logf("Concurrency: %d", result.Concurrency)
	s.T().Logf("Total requests: %d", result.TotalRequests)
	s.T().Logf("Successful requests: %d", result.SuccessfulRequests)
	s.T().Logf("Failed requests: %d", result.FailedRequests)
	s.T().Logf("Requests per second: %.2f", result.RequestsPerSecond)

	// Assertions
	s.Greater(result.TotalRequests, int64(0), "Should have made requests")
	s.Greater(result.RequestsPerSecond, float64(50), "Should handle at least 50 requests per second")

	// Calculate success rate
	successRate := float64(result.SuccessfulRequests) / float64(result.TotalRequests) * 100
	s.Greater(successRate, 95.0, "Success rate should be above 95%")

	// Calculate average response time
	if len(result.Requests) > 0 {
		var totalDuration time.Duration
		for _, req := range result.Requests {
			if req.Error == nil {
				totalDuration += req.Duration
			}
		}
		avgDuration := totalDuration / time.Duration(result.SuccessfulRequests)
		s.T().Logf("Average response time: %v", avgDuration)

		// Response time should be reasonable
		s.Less(avgDuration, 100*time.Millisecond, "Average response time should be under 100ms")
	}
}

// TestConcurrentGistCreation tests concurrent gist creation
func (s *PerformanceTestSuite) TestConcurrentGistCreation() {
	// Create test user
	user := s.TestData.CreateTestUserWithData(s.T(), map[string]interface{}{
		"username": "concurrentuser",
		"password": "TestPassword123!",
	})

	// Login
	err := s.APIClient.Login(user.Username, "TestPassword123!")
	s.Require().NoError(err)

	concurrency := 5
	gistsPerWorker := 3

	var wg sync.WaitGroup
	var mu sync.Mutex
	results := make([]bool, 0)

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()

			for j := 0; j < gistsPerWorker; j++ {
				gistData := map[string]interface{}{
					"title":       "Concurrent Test Gist",
					"description": "Created by concurrent test",
					"visibility":  "public",
					"files": []map[string]interface{}{
						{
							"filename": "test.txt",
							"content":  "Test content from worker",
						},
					},
				}

				var createdGist map[string]interface{}
				err := s.APIClient.PostJSON("/api/v1/gists", gistData, &createdGist)

				mu.Lock()
				results = append(results, err == nil)
				mu.Unlock()
			}
		}(i)
	}

	wg.Wait()

	// Analyze results
	successCount := 0
	for _, success := range results {
		if success {
			successCount++
		}
	}

	expectedTotal := concurrency * gistsPerWorker
	successRate := float64(successCount) / float64(expectedTotal) * 100

	s.T().Logf("Concurrent gist creation results:")
	s.T().Logf("Total attempts: %d", expectedTotal)
	s.T().Logf("Successful: %d", successCount)
	s.T().Logf("Success rate: %.2f%%", successRate)

	// Should have high success rate
	s.Greater(successRate, 90.0, "Success rate should be above 90%")

	// Verify actual count in database
	s.AssertDatabaseCount(&user, 1) // Still only one user

	// Count gists in database
	var gistCount int64
	err = s.DB.Model(&testingSuite.TestDataManager{}).Where("user_id = ?", user.ID).Count(&gistCount).Error
	s.Require().NoError(err)

	s.T().Logf("Gists in database: %d", gistCount)
	s.Equal(int64(successCount), gistCount, "Database count should match successful creates")
}

// TestMemoryUsage tests for memory leaks and excessive usage
func (s *PerformanceTestSuite) TestMemoryUsage() {
	// This is a basic memory usage test
	// In production, you'd use more sophisticated profiling tools

	// Create test data
	user := s.TestData.CreateTestUserWithData(s.T(), map[string]interface{}{
		"username": "memoryuser",
		"password": "TestPassword123!",
	})

	err := s.APIClient.Login(user.Username, "TestPassword123!")
	s.Require().NoError(err)

	// Make many requests and check that they complete without OOM
	requestCount := 100
	errors := 0

	for i := 0; i < requestCount; i++ {
		resp, err := s.APIClient.GET("/api/v1/gists")
		if err != nil {
			errors++
			continue
		}
		resp.Body.Close()

		if resp.StatusCode >= 400 {
			errors++
		}
	}

	errorRate := float64(errors) / float64(requestCount) * 100
	s.T().Logf("Memory test results:")
	s.T().Logf("Total requests: %d", requestCount)
	s.T().Logf("Errors: %d", errors)
	s.T().Logf("Error rate: %.2f%%", errorRate)

	s.Less(errorRate, 5.0, "Error rate should be low during memory test")
}

// TestDatabasePerformance tests database operation performance
func (s *PerformanceTestSuite) TestDatabasePerformance() {
	// Test bulk inserts
	user := s.TestData.CreateTestUserWithData(s.T(), map[string]interface{}{
		"username": "dbperfuser",
	})

	gistCount := 50
	start := time.Now()

	for i := 0; i < gistCount; i++ {
		s.TestData.CreateTestGist(s.T(), user, map[string]interface{}{
			"title": "DB Performance Test Gist",
		})
	}

	insertDuration := time.Since(start)
	s.T().Logf("Inserted %d gists in %v", gistCount, insertDuration)

	// Test queries
	start = time.Now()

	var gists []interface{}
	err := s.DB.Where("user_id = ?", user.ID).Find(&gists).Error
	s.Require().NoError(err)

	queryDuration := time.Since(start)
	s.T().Logf("Queried %d gists in %v", len(gists), queryDuration)

	// Performance assertions
	avgInsertTime := insertDuration / time.Duration(gistCount)
	s.Less(avgInsertTime, 10*time.Millisecond, "Average insert time should be under 10ms")

	s.Less(queryDuration, 50*time.Millisecond, "Query time should be under 50ms")
}

// TestResponseTimes tests API response times under various conditions
func (s *PerformanceTestSuite) TestResponseTimes() {
	endpoints := []string{
		"/health",
		"/healthz",
		"/api/v1/gists",
	}

	measurements := make(map[string][]time.Duration)

	// Warm up
	for _, endpoint := range endpoints {
		resp, err := s.APIClient.GET(endpoint)
		if err == nil {
			resp.Body.Close()
		}
	}

	// Measure response times
	iterations := 10
	for _, endpoint := range endpoints {
		measurements[endpoint] = make([]time.Duration, 0, iterations)

		for i := 0; i < iterations; i++ {
			start := time.Now()
			resp, err := s.APIClient.GET(endpoint)
			duration := time.Since(start)

			if err == nil {
				resp.Body.Close()
				if resp.StatusCode < 400 {
					measurements[endpoint] = append(measurements[endpoint], duration)
				}
			}
		}
	}

	// Analyze results
	for endpoint, durations := range measurements {
		if len(durations) == 0 {
			continue
		}

		var total time.Duration
		var max time.Duration
		min := durations[0]

		for _, d := range durations {
			total += d
			if d > max {
				max = d
			}
			if d < min {
				min = d
			}
		}

		avg := total / time.Duration(len(durations))

		s.T().Logf("Endpoint %s response times:", endpoint)
		s.T().Logf("  Average: %v", avg)
		s.T().Logf("  Min: %v", min)
		s.T().Logf("  Max: %v", max)
		s.T().Logf("  Samples: %d", len(durations))

		// Basic performance assertions
		s.Less(avg, 200*time.Millisecond, "Average response time should be reasonable")
		s.Less(max, 1*time.Second, "Max response time should not be excessive")
	}
}
