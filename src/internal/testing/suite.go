package testing

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/casapps/casgist/src/internal/auth"
	"github.com/casapps/casgist/src/internal/database"
	"github.com/casapps/casgist/src/internal/database/models"
	"github.com/casapps/casgist/src/internal/server"
)

// TestSuite provides a comprehensive testing framework for CasGists
type TestSuite struct {
	suite.Suite
	
	// Core components
	DB           *gorm.DB
	Config       *viper.Viper
	Server       *server.Server
	Echo         *echo.Echo
	TestServer   *httptest.Server
	
	// Test utilities
	TempDir      string
	TestData     *TestDataManager
	APIClient    *APITestClient
	
	// Cleanup functions
	cleanupFuncs []func()
	mu           sync.RWMutex
}

// TestDataManager manages test data creation and cleanup
type TestDataManager struct {
	db       *gorm.DB
	users    []models.User
	gists    []models.Gist
	files    []models.GistFile
	mu       sync.RWMutex
}

// APITestClient provides utilities for API testing
type APITestClient struct {
	baseURL    string
	httpClient *http.Client
	authToken  string
	csrf       string
}

// SetupSuite initializes the test suite
func (s *TestSuite) SetupSuite() {
	// Create temporary directory
	tempDir, err := os.MkdirTemp("", "casgists-test-*")
	require.NoError(s.T(), err)
	s.TempDir = tempDir
	
	// Initialize configuration
	s.setupConfig()
	
	// Initialize database
	s.setupDatabase()
	
	// Initialize server
	s.setupServer()
	
	// Initialize test utilities
	s.setupTestUtilities()
	
	s.T().Logf("Test suite initialized with temp directory: %s", s.TempDir)
}

// TearDownSuite cleans up the test suite
func (s *TestSuite) TearDownSuite() {
	// Run all cleanup functions
	s.mu.RLock()
	cleanupFuncs := make([]func(), len(s.cleanupFuncs))
	copy(cleanupFuncs, s.cleanupFuncs)
	s.mu.RUnlock()
	
	for i := len(cleanupFuncs) - 1; i >= 0; i-- {
		if cleanupFuncs[i] != nil {
			cleanupFuncs[i]()
		}
	}
	
	// Close test server
	if s.TestServer != nil {
		s.TestServer.Close()
	}
	
	// Close database
	if s.DB != nil {
		sqlDB, _ := s.DB.DB()
		if sqlDB != nil {
			sqlDB.Close()
		}
	}
	
	// Remove temporary directory
	if s.TempDir != "" {
		os.RemoveAll(s.TempDir)
	}
	
	s.T().Log("Test suite cleanup completed")
}

// SetupTest runs before each test
func (s *TestSuite) SetupTest() {
	// Clean up test data
	s.TestData.CleanupAll()
	
	// Reset server state if needed
	// Add any per-test setup here
}

// TearDownTest runs after each test
func (s *TestSuite) TearDownTest() {
	// Cleanup test-specific data
	s.TestData.CleanupAll()
	
	// Reset authentication
	s.APIClient.authToken = ""
	s.APIClient.csrf = ""
}

// AddCleanup adds a cleanup function to be called during teardown
func (s *TestSuite) AddCleanup(fn func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cleanupFuncs = append(s.cleanupFuncs, fn)
}

// setupConfig initializes test configuration
func (s *TestSuite) setupConfig() {
	config := viper.New()
	config.SetDefault("environment", "test")
	config.SetDefault("database.driver", "sqlite")
	config.SetDefault("database.dsn", ":memory:")
	config.SetDefault("server.host", "localhost")
	config.SetDefault("server.port", 0) // Random port
	config.SetDefault("security.secret_key", "test-secret-key-for-testing-only-do-not-use-in-production")
	config.SetDefault("security.jwt.access_token_ttl", "2h")
	config.SetDefault("security.jwt.refresh_token_ttl", "24h")
	config.SetDefault("security.disable_csrf", true) // Disable CSRF for testing
	config.SetDefault("ui.title", "CasGists Test")
	config.SetDefault("auth.allow_registration", true)
	config.SetDefault("auth.require_email_verification", false)
	config.SetDefault("features.registration", true)
	config.SetDefault("features.public_gists", true)
	config.SetDefault("features.search", false) // Disable search for testing to avoid FTS5 issues
	config.SetDefault("logging.level", "error")
	config.SetDefault("logging.directory", filepath.Join(s.TempDir, "logs"))
	config.SetDefault("storage.path", filepath.Join(s.TempDir, "storage"))
	config.SetDefault("git.path", filepath.Join(s.TempDir, "git"))
	config.SetDefault("version", "test")
	
	s.Config = config
}

// setupDatabase initializes test database
func (s *TestSuite) setupDatabase() {
	// Use in-memory SQLite for testing
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent), // Suppress SQL logs in tests
		NowFunc: func() time.Time {
			return time.Now().UTC()
		},
	})
	require.NoError(s.T(), err)
	
	// Auto-migrate all models (skip FTS for testing)
	err = database.MigrateTestDB(db)
	require.NoError(s.T(), err)
	
	s.DB = db
	
	// Add cleanup
	s.AddCleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			sqlDB.Close()
		}
	})
}

// setupServer initializes test server
func (s *TestSuite) setupServer() {
	// Create Echo instance
	e := echo.New()
	e.HideBanner = true
	
	// Create server
	srv := server.NewWithPaths(e, s.Config, s.DB, nil)
	
	// Create HTTP test server
	testServer := httptest.NewServer(e)
	
	s.Echo = e
	s.Server = srv
	s.TestServer = testServer
	
	// Update config with test server URL
	s.Config.Set("server.url", testServer.URL)
	
	s.AddCleanup(func() {
		testServer.Close()
	})
}

// setupTestUtilities initializes test utilities
func (s *TestSuite) setupTestUtilities() {
	s.TestData = &TestDataManager{
		db:    s.DB,
		users: make([]models.User, 0),
		gists: make([]models.Gist, 0),
		files: make([]models.GistFile, 0),
	}
	
	s.APIClient = &APITestClient{
		baseURL:    s.TestServer.URL,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
	
	// Create default test users
	s.createDefaultTestUsers()
}

// createDefaultTestUsers creates standard test users
func (s *TestSuite) createDefaultTestUsers() {
	// Create a test user for authentication tests
	testUser, err := s.TestData.CreateTestUser("testuser", "test@example.com", "password123")
	if err != nil {
		s.T().Logf("Failed to create test user: %v", err)
	} else {
		s.T().Logf("Created test user: %s", testUser.Username)
	}
	
	// Create an admin user
	adminUser, err := s.TestData.CreateTestUser("admin", "admin@example.com", "admin123")
	if err != nil {
		s.T().Logf("Failed to create admin user: %v", err)
	} else {
		adminUser.IsAdmin = true
		s.DB.Save(&adminUser)
		s.T().Logf("Created admin user: %s", adminUser.Username)
	}
}

// Test Data Manager Methods

// CreateTestUser creates a test user with username, email, and password
func (tm *TestDataManager) CreateTestUser(username, email, password string) (*models.User, error) {
	passwordHash, err := auth.HashPassword(password)
	if err != nil {
		return nil, err
	}
	
	user := models.User{
		Username:     username,
		Email:        email,
		PasswordHash: passwordHash,
		IsActive:     true,
	}
	
	if err := tm.db.Create(&user).Error; err != nil {
		return nil, err
	}
	
	tm.mu.Lock()
	tm.users = append(tm.users, user)
	tm.mu.Unlock()
	
	return &user, nil
}

// CreateTestUserWithData creates a test user from data map
func (tm *TestDataManager) CreateTestUserWithData(t *testing.T, data map[string]interface{}) *models.User {
	user := models.User{
		Username: getStringOrDefault(data, "username", fmt.Sprintf("testuser%d", time.Now().UnixNano())),
		Email:    getStringOrDefault(data, "email", fmt.Sprintf("test%d@example.com", time.Now().UnixNano())),
		IsActive: true,
	}
	
	// Hash password if provided
	password := getStringOrDefault(data, "password", "DefaultTestPassword123!")
	passwordHash, err := auth.HashPassword(password)
	require.NoError(t, err)
	user.PasswordHash = passwordHash
	
	if isAdmin, ok := data["is_admin"].(bool); ok {
		user.IsAdmin = isAdmin
	}
	
	err = tm.db.Create(&user).Error
	require.NoError(t, err)
	
	tm.mu.Lock()
	tm.users = append(tm.users, user)
	tm.mu.Unlock()
	
	return &user
}

// CreateTestGist creates a test gist
func (tm *TestDataManager) CreateTestGist(t *testing.T, user *models.User, data map[string]interface{}) *models.Gist {
	gist := models.Gist{
		Title:       getStringOrDefault(data, "title", "Test Gist"),
		Description: getStringOrDefault(data, "description", "Test gist description"),
		Visibility:  models.Visibility(getStringOrDefault(data, "visibility", "public")),
		UserID:      &user.ID,
	}
	
	err := tm.db.Create(&gist).Error
	require.NoError(t, err)
	
	// Create files if provided
	if filesData, ok := data["files"].([]map[string]interface{}); ok {
		for _, fileData := range filesData {
			tm.CreateTestFile(t, &gist, fileData)
		}
	}
	
	tm.mu.Lock()
	tm.gists = append(tm.gists, gist)
	tm.mu.Unlock()
	
	return &gist
}

// CreateTestFile creates a test file
func (tm *TestDataManager) CreateTestFile(t *testing.T, gist *models.Gist, data map[string]interface{}) *models.GistFile {
	file := models.GistFile{
		Filename: getStringOrDefault(data, "filename", "test.txt"),
		Content:  getStringOrDefault(data, "content", "Test file content"),
		Size:     int64(len(getStringOrDefault(data, "content", "Test file content"))),
		GistID:   gist.ID,
	}
	
	err := tm.db.Create(&file).Error
	require.NoError(t, err)
	
	tm.mu.Lock()
	tm.files = append(tm.files, file)
	tm.mu.Unlock()
	
	return &file
}

// CleanupAll removes all test data
func (tm *TestDataManager) CleanupAll() {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	
	// Delete in reverse order of dependencies
	for _, file := range tm.files {
		tm.db.Unscoped().Delete(&file)
	}
	
	for _, gist := range tm.gists {
		tm.db.Unscoped().Delete(&gist)
	}
	
	for _, user := range tm.users {
		tm.db.Unscoped().Delete(&user)
	}
	
	// Clear slices
	tm.files = tm.files[:0]
	tm.gists = tm.gists[:0]
	tm.users = tm.users[:0]
}

// API Test Client Methods

// Login authenticates with the test API
func (c *APITestClient) Login(username, password string) error {
	loginData := map[string]string{
		"username": username,
		"password": password,
	}
	
	resp, err := c.POST("/api/v1/auth/login", loginData)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("login failed with status %d", resp.StatusCode)
	}
	
	// Parse response to extract JWT token
	var loginResp map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&loginResp); err != nil {
		return fmt.Errorf("failed to parse login response: %w", err)
	}
	
	// Extract access token
	if accessToken, ok := loginResp["access_token"].(string); ok && accessToken != "" {
		c.authToken = accessToken
		return nil
	}
	
	// Fallback: try to extract from session cookie
	for _, cookie := range resp.Cookies() {
		if cookie.Name == "session" {
			c.authToken = cookie.Value
			return nil
		}
	}
	
	return fmt.Errorf("no authentication token found in response")
}

// Logout clears the authentication token
func (c *APITestClient) Logout() {
	c.authToken = ""
}

// GET performs a GET request
func (c *APITestClient) GET(path string) (*http.Response, error) {
	return c.request("GET", path, nil)
}

// POST performs a POST request
func (c *APITestClient) POST(path string, data interface{}) (*http.Response, error) {
	return c.request("POST", path, data)
}

// PUT performs a PUT request
func (c *APITestClient) PUT(path string, data interface{}) (*http.Response, error) {
	return c.request("PUT", path, data)
}

// DELETE performs a DELETE request
func (c *APITestClient) DELETE(path string) (*http.Response, error) {
	return c.request("DELETE", path, nil)
}

// request performs HTTP request with authentication
func (c *APITestClient) request(method, path string, data interface{}) (*http.Response, error) {
	var body io.Reader
	
	if data != nil {
		jsonData, err := json.Marshal(data)
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(jsonData)
	}
	
	req, err := http.NewRequest(method, c.baseURL+path, body)
	if err != nil {
		return nil, err
	}
	
	// Add authentication
	if c.authToken != "" {
		// Use Bearer token for JWT authentication
		req.Header.Set("Authorization", "Bearer "+c.authToken)
	}
	
	// Add CSRF token if available
	if c.csrf != "" {
		req.Header.Set("X-CSRF-Token", c.csrf)
	}
	
	// Set content type for data requests
	if data != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	
	return c.httpClient.Do(req)
}

// GetJSON gets and unmarshals JSON response
func (c *APITestClient) GetJSON(path string, target interface{}) error {
	resp, err := c.GET(path)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	
	if resp.StatusCode >= 400 {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, resp.Status)
	}
	
	return json.NewDecoder(resp.Body).Decode(target)
}

// PostJSON posts JSON and unmarshals response
func (c *APITestClient) PostJSON(path string, data interface{}, target interface{}) error {
	resp, err := c.POST(path, data)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("HTTP %d: %s - %s", resp.StatusCode, resp.Status, string(body))
	}
	
	if target != nil {
		return json.NewDecoder(resp.Body).Decode(target)
	}
	
	return nil
}

// PutJSON puts JSON and unmarshals response
func (c *APITestClient) PutJSON(path string, data interface{}, target interface{}) error {
	resp, err := c.PUT(path, data)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("HTTP %d: %s - %s", resp.StatusCode, resp.Status, string(body))
	}
	
	if target != nil {
		return json.NewDecoder(resp.Body).Decode(target)
	}
	
	return nil
}

// Test Assertions

// AssertAPIError asserts that an API call returns a specific error
func (s *TestSuite) AssertAPIError(resp *http.Response, expectedCode int, expectedMessage string) {
	assert.Equal(s.T(), expectedCode, resp.StatusCode)
	
	var errorResp map[string]interface{}
	err := json.NewDecoder(resp.Body).Decode(&errorResp)
	require.NoError(s.T(), err)
	
	if expectedMessage != "" {
		assert.Contains(s.T(), errorResp["error"].(string), expectedMessage)
	}
}

// AssertValidationError asserts that a validation error is returned
func (s *TestSuite) AssertValidationError(resp *http.Response, field string) {
	assert.Equal(s.T(), http.StatusBadRequest, resp.StatusCode)
	
	var errorResp map[string]interface{}
	err := json.NewDecoder(resp.Body).Decode(&errorResp)
	require.NoError(s.T(), err)
	
	assert.Equal(s.T(), "VALIDATION_FAILED", errorResp["code"])
	
	if details, ok := errorResp["details"].(map[string]interface{}); ok {
		if validationErrors, ok := details["validation_errors"].([]interface{}); ok {
			found := false
			for _, ve := range validationErrors {
				if errMap, ok := ve.(map[string]interface{}); ok {
					if errMap["field"].(string) == field {
						found = true
						break
					}
				}
			}
			assert.True(s.T(), found, fmt.Sprintf("Validation error for field '%s' not found", field))
		}
	}
}

// AssertDatabaseCount asserts the count of records in database
func (s *TestSuite) AssertDatabaseCount(model interface{}, expectedCount int64) {
	var count int64
	err := s.DB.Model(model).Count(&count).Error
	require.NoError(s.T(), err)
	assert.Equal(s.T(), expectedCount, count)
}

// Performance Test Utilities

// BenchmarkAPI benchmarks an API endpoint
func (s *TestSuite) BenchmarkAPI(b *testing.B, method, path string, data interface{}) {
	b.Helper()
	
	client := s.APIClient
	
	b.ResetTimer()
	b.ReportAllocs()
	
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			var resp *http.Response
			var err error
			
			switch strings.ToUpper(method) {
			case "GET":
				resp, err = client.GET(path)
			case "POST":
				resp, err = client.POST(path, data)
			case "PUT":
				resp, err = client.PUT(path, data)
			case "DELETE":
				resp, err = client.DELETE(path)
			default:
				b.Fatalf("Unsupported HTTP method: %s", method)
			}
			
			if err != nil {
				b.Fatal(err)
			}
			
			resp.Body.Close()
			
			if resp.StatusCode >= 400 {
				b.Fatalf("HTTP %d: %s", resp.StatusCode, resp.Status)
			}
		}
	})
}

// LoadTest performs load testing on an endpoint
func (s *TestSuite) LoadTest(endpoint string, duration time.Duration, concurrency int) *LoadTestResult {
	result := &LoadTestResult{
		StartTime:   time.Now(),
		Duration:    duration,
		Concurrency: concurrency,
		Requests:    make([]RequestResult, 0),
	}
	
	ctx, cancel := context.WithTimeout(context.Background(), duration)
	defer cancel()
	
	var wg sync.WaitGroup
	requestsChan := make(chan RequestResult, 1000)
	
	// Start workers
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			
			client := &APITestClient{
				baseURL:    s.TestServer.URL,
				httpClient: &http.Client{Timeout: 10 * time.Second},
			}
			
			for {
				select {
				case <-ctx.Done():
					return
				default:
					start := time.Now()
					resp, err := client.GET(endpoint)
					duration := time.Since(start)
					
					reqResult := RequestResult{
						Duration:   duration,
						StatusCode: 0,
						Error:      err,
					}
					
					if resp != nil {
						reqResult.StatusCode = resp.StatusCode
						resp.Body.Close()
					}
					
					requestsChan <- reqResult
				}
			}
		}()
	}
	
	// Collect results
	go func() {
		wg.Wait()
		close(requestsChan)
	}()
	
	for reqResult := range requestsChan {
		result.Requests = append(result.Requests, reqResult)
		if reqResult.Error == nil {
			result.SuccessfulRequests++
		} else {
			result.FailedRequests++
		}
	}
	
	result.EndTime = time.Now()
	result.TotalRequests = result.SuccessfulRequests + result.FailedRequests
	result.RequestsPerSecond = float64(result.TotalRequests) / result.Duration.Seconds()
	
	return result
}

// LoadTestResult contains load test results
type LoadTestResult struct {
	StartTime          time.Time
	EndTime            time.Time
	Duration           time.Duration
	Concurrency        int
	TotalRequests      int64
	SuccessfulRequests int64
	FailedRequests     int64
	RequestsPerSecond  float64
	Requests           []RequestResult
}

// RequestResult contains individual request result
type RequestResult struct {
	Duration   time.Duration
	StatusCode int
	Error      error
}

// Helper functions

func getStringOrDefault(data map[string]interface{}, key, defaultValue string) string {
	if val, ok := data[key].(string); ok {
		return val
	}
	return defaultValue
}