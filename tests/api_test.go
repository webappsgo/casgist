//go:build integration
// +build integration

package tests

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/suite"

	testingSuite "github.com/casapps/casgist/src/internal/testing"
)

// APITestSuite tests the API endpoints
type APITestSuite struct {
	testingSuite.TestSuite
}

// TestAPITestSuite runs the API test suite
func TestAPITestSuite(t *testing.T) {
	suite.Run(t, new(APITestSuite))
}

// Test Health Check
func (s *APITestSuite) TestHealthCheck() {
	resp, err := s.APIClient.GET("/health")
	s.Require().NoError(err)
	defer resp.Body.Close()

	s.Equal(http.StatusOK, resp.StatusCode)

	var health map[string]interface{}
	err = json.NewDecoder(resp.Body).Decode(&health)
	s.Require().NoError(err)

	s.Equal("ok", health["status"])
	s.NotEmpty(health["timestamp"])
	s.NotEmpty(health["version"])
}

// Test Enhanced Health Check
func (s *APITestSuite) TestEnhancedHealthCheck() {
	resp, err := s.APIClient.GET("/healthz")
	s.Require().NoError(err)
	defer resp.Body.Close()

	s.Equal(http.StatusOK, resp.StatusCode)

	var health map[string]interface{}
	err = json.NewDecoder(resp.Body).Decode(&health)
	s.Require().NoError(err)

	s.Equal("healthy", health["status"])
	s.Contains(health, "components")
	s.Contains(health, "metrics")

	// Check components
	components := health["components"].(map[string]interface{})
	s.Equal("healthy", components["database"])
}

// Test User Registration and Authentication
func (s *APITestSuite) TestUserAuthentication() {
	// Test user registration
	userData := map[string]string{
		"username": "testuser",
		"email":    "test@example.com",
		"password": "TestPassword123!",
	}

	resp, err := s.APIClient.POST("/api/v1/auth/register", userData)
	s.Require().NoError(err)
	defer resp.Body.Close()

	s.Equal(http.StatusCreated, resp.StatusCode)

	var user map[string]interface{}
	err = json.NewDecoder(resp.Body).Decode(&user)
	s.Require().NoError(err)

	s.Equal("testuser", user["username"])
	s.Equal("test@example.com", user["email"])
	s.NotContains(user, "password") // Password should not be returned

	// Test login
	loginData := map[string]string{
		"username": "testuser",
		"password": "TestPassword123!",
	}

	resp, err = s.APIClient.POST("/api/v1/auth/login", loginData)
	s.Require().NoError(err)
	defer resp.Body.Close()

	s.Equal(http.StatusOK, resp.StatusCode)

	// Test getting current user (should be authenticated now)
	err = s.APIClient.Login("testuser", "TestPassword123!")
	s.Require().NoError(err)

	resp, err = s.APIClient.GET("/api/v1/user")
	s.Require().NoError(err)
	defer resp.Body.Close()

	s.Equal(http.StatusOK, resp.StatusCode)
}

// Test Gist Operations
func (s *APITestSuite) TestGistOperations() {
	// Create test user
	user := s.TestData.CreateTestUserWithData(s.T(), map[string]interface{}{
		"username": "gistuser",
		"password": "TestPassword123!",
	})

	// Login
	err := s.APIClient.Login(user.Username, "TestPassword123!")
	s.Require().NoError(err)

	// Test creating a gist
	gistData := map[string]interface{}{
		"title":       "Test Gist",
		"description": "A test gist",
		"visibility":  "public",
		"files": []map[string]interface{}{
			{
				"filename": "test.txt",
				"content":  "Hello, world!",
			},
			{
				"filename": "code.js",
				"content":  "console.log('Hello from JavaScript');",
			},
		},
	}

	var createdGist map[string]interface{}
	err = s.APIClient.PostJSON("/api/v1/gists", gistData, &createdGist)
	s.Require().NoError(err)

	s.Equal("Test Gist", createdGist["title"])
	s.Equal("A test gist", createdGist["description"])
	s.Equal("public", createdGist["visibility"])

	gistID := createdGist["id"].(string)
	s.NotEmpty(gistID)

	// Test getting the gist
	var fetchedGist map[string]interface{}
	err = s.APIClient.GetJSON("/api/v1/gists/"+gistID, &fetchedGist)
	s.Require().NoError(err)

	s.Equal("Test Gist", fetchedGist["title"])

	// Check files
	files := fetchedGist["files"].([]interface{})
	s.Len(files, 2)

	// Test updating the gist
	updateData := map[string]interface{}{
		"title":       "Updated Test Gist",
		"description": "An updated test gist",
		"files": []map[string]interface{}{
			{
				"filename": "test.txt",
				"content":  "Hello, updated world!",
			},
		},
	}

	var updatedGist map[string]interface{}
	err = s.APIClient.PostJSON("/api/v1/gists/"+gistID, updateData, &updatedGist)
	s.Require().NoError(err)

	s.Equal("Updated Test Gist", updatedGist["title"])

	// Test listing gists
	var gistList map[string]interface{}
	err = s.APIClient.GetJSON("/api/v1/gists", &gistList)
	s.Require().NoError(err)

	gists := gistList["gists"].([]interface{})
	s.GreaterOrEqual(len(gists), 1)

	// Test deleting the gist
	resp, err := s.APIClient.DELETE("/api/v1/gists/" + gistID)
	s.Require().NoError(err)
	resp.Body.Close()

	s.Equal(http.StatusNoContent, resp.StatusCode)

	// Verify deletion
	resp, err = s.APIClient.GET("/api/v1/gists/" + gistID)
	s.Require().NoError(err)
	resp.Body.Close()

	s.Equal(http.StatusNotFound, resp.StatusCode)
}

// Test Search Functionality
func (s *APITestSuite) TestSearchFunctionality() {
	// Create test user and gists
	user := s.TestData.CreateTestUserWithData(s.T(), map[string]interface{}{
		"username": "searchuser",
	})

	// Create searchable gists
	gist1 := s.TestData.CreateTestGist(s.T(), user, map[string]interface{}{
		"title":       "JavaScript Tutorial",
		"description": "Learn JavaScript basics",
		"visibility":  "public",
		"files": []map[string]interface{}{
			{
				"filename": "tutorial.js",
				"content":  "function hello() { console.log('Hello, JavaScript!'); }",
			},
		},
	})

	gist2 := s.TestData.CreateTestGist(s.T(), user, map[string]interface{}{
		"title":       "Python Guide",
		"description": "Python programming guide",
		"visibility":  "public",
		"files": []map[string]interface{}{
			{
				"filename": "guide.py",
				"content":  "def hello(): print('Hello, Python!')",
			},
		},
	})

	// Test search
	var searchResults map[string]interface{}
	err := s.APIClient.GetJSON("/api/v1/search?q=JavaScript", &searchResults)
	s.Require().NoError(err)

	results := searchResults["results"].([]interface{})
	s.GreaterOrEqual(len(results), 1)

	// Verify search results contain our gist
	found := false
	for _, result := range results {
		resultMap := result.(map[string]interface{})
		if resultMap["id"].(string) == gist1.ID.String() {
			found = true
			break
		}
	}
	s.True(found, "JavaScript gist should be found in search results")

	// Test search with no results
	err = s.APIClient.GetJSON("/api/v1/search?q=nonexistent", &searchResults)
	s.Require().NoError(err)

	results = searchResults["results"].([]interface{})
	s.Equal(0, len(results))

	// Avoid unused variable error
	_ = gist2
}

// Test Input Validation
func (s *APITestSuite) TestInputValidation() {
	// Test invalid email registration
	invalidUserData := map[string]string{
		"username": "testuser",
		"email":    "invalid-email",
		"password": "TestPassword123!",
	}

	resp, err := s.APIClient.POST("/api/v1/auth/register", invalidUserData)
	s.Require().NoError(err)
	defer resp.Body.Close()

	s.AssertValidationError(resp, "email")

	// Test weak password
	weakPasswordData := map[string]string{
		"username": "testuser",
		"email":    "test@example.com",
		"password": "weak",
	}

	resp, err = s.APIClient.POST("/api/v1/auth/register", weakPasswordData)
	s.Require().NoError(err)
	defer resp.Body.Close()

	s.AssertValidationError(resp, "password")

	// Test missing required fields for gist
	user := s.TestData.CreateTestUserWithData(s.T(), map[string]interface{}{
		"username": "validationuser",
		"password": "TestPassword123!",
	})

	err = s.APIClient.Login(user.Username, "TestPassword123!")
	s.Require().NoError(err)

	invalidGistData := map[string]interface{}{
		"description": "Missing title",
		"files":       []map[string]interface{}{},
	}

	resp, err = s.APIClient.POST("/api/v1/gists", invalidGistData)
	s.Require().NoError(err)
	defer resp.Body.Close()

	s.AssertValidationError(resp, "title")
}

// Test Rate Limiting
func (s *APITestSuite) TestRateLimit() {
	// This test depends on rate limiting configuration
	// Make multiple requests quickly to trigger rate limiting

	endpoint := "/api/v1/gists"
	attempts := 100 // Adjust based on rate limit configuration

	rateLimitHit := false
	for i := 0; i < attempts; i++ {
		resp, err := s.APIClient.GET(endpoint)
		if err != nil {
			continue
		}

		if resp.StatusCode == http.StatusTooManyRequests {
			rateLimitHit = true
			resp.Body.Close()
			break
		}

		resp.Body.Close()
	}

	// Note: Rate limiting might not be hit in tests due to configuration
	// This is more of a smoke test
	s.T().Logf("Rate limiting test completed. Rate limit hit: %v", rateLimitHit)
}

// Test Error Handling
func (s *APITestSuite) TestErrorHandling() {
	// Test 404 error
	resp, err := s.APIClient.GET("/api/v1/gists/nonexistent-id")
	s.Require().NoError(err)
	defer resp.Body.Close()

	s.AssertAPIError(resp, http.StatusNotFound, "")

	// Test unauthorized access
	resp, err = s.APIClient.GET("/api/v1/user")
	s.Require().NoError(err)
	defer resp.Body.Close()

	s.Equal(http.StatusUnauthorized, resp.StatusCode)

	// Test malformed JSON
	resp, err = s.APIClient.POST("/api/v1/auth/register", "invalid json")
	s.Require().NoError(err)
	defer resp.Body.Close()

	s.Equal(http.StatusBadRequest, resp.StatusCode)
}
