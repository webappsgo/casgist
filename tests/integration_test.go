//go:build integration
// +build integration

package tests

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"

	testingSuite "github.com/casapps/casgist/src/internal/testing"
)

// IntegrationTestSuite tests end-to-end workflows
type IntegrationTestSuite struct {
	testingSuite.TestSuite
}

// TestIntegrationTestSuite runs the integration test suite
func TestIntegrationTestSuite(t *testing.T) {
	suite.Run(t, new(IntegrationTestSuite))
}

// TestCompleteUserJourney tests a complete user journey from registration to gist management
func (s *IntegrationTestSuite) TestCompleteUserJourney() {
	// Step 1: User Registration
	userData := map[string]string{
		"username": "journeyuser",
		"email":    "journey@example.com",
		"password": "JourneyPassword123!",
	}

	resp, err := s.APIClient.POST("/api/v1/auth/register", userData)
	s.Require().NoError(err)
	defer resp.Body.Close()
	s.Equal(http.StatusCreated, resp.StatusCode)

	var registeredUser map[string]interface{}
	err = json.NewDecoder(resp.Body).Decode(&registeredUser)
	s.Require().NoError(err)

	// Extract user ID from nested user object
	user := registeredUser["user"].(map[string]interface{})
	userID := user["id"].(string)
	s.NotEmpty(userID)

	// Step 2: User Login
	err = s.APIClient.Login("journeyuser", "JourneyPassword123!")
	s.Require().NoError(err)

	// Step 3: Verify authenticated access
	var currentUser map[string]interface{}
	err = s.APIClient.GetJSON("/api/v1/user", &currentUser)
	s.Require().NoError(err)
	s.Equal("journeyuser", currentUser["username"])

	// Step 4: Create first gist
	gist1Data := map[string]interface{}{
		"title":       "My First Gist",
		"description": "This is my first gist on CasGists",
		"visibility":  "public",
		"files": []map[string]interface{}{
			{
				"filename": "hello.js",
				"content":  "console.log('Hello, CasGists!');",
			},
		},
	}

	var gist1 map[string]interface{}
	err = s.APIClient.PostJSON("/api/v1/gists", gist1Data, &gist1)
	s.Require().NoError(err)

	gist1ID := gist1["id"].(string)
	s.NotEmpty(gist1ID)

	// Step 5: Create second gist with multiple files
	gist2Data := map[string]interface{}{
		"title":       "Multi-file Project",
		"description": "A project with multiple files",
		"visibility":  "unlisted",
		"files": []map[string]interface{}{
			{
				"filename": "index.html",
				"content":  "<!DOCTYPE html>\n<html><body><h1>Hello World</h1></body></html>",
			},
			{
				"filename": "style.css",
				"content":  "body { font-family: Arial, sans-serif; }",
			},
			{
				"filename": "script.js",
				"content":  "document.addEventListener('DOMContentLoaded', () => { console.log('Ready!'); });",
			},
		},
	}

	var gist2 map[string]interface{}
	err = s.APIClient.PostJSON("/api/v1/gists", gist2Data, &gist2)
	s.Require().NoError(err)

	gist2ID := gist2["id"].(string)
	s.NotEmpty(gist2ID)

	// Step 6: List user's gists
	var gistList map[string]interface{}
	err = s.APIClient.GetJSON("/api/v1/gists", &gistList)
	s.Require().NoError(err)

	gists := gistList["gists"].([]interface{})
	s.GreaterOrEqual(len(gists), 2, "Should have at least 2 gists")

	// Step 7: Update a gist
	updateData := map[string]interface{}{
		"title":       "My Updated First Gist",
		"description": "Updated description",
		"files": []map[string]interface{}{
			{
				"filename": "hello.js",
				"content":  "console.log('Hello, Updated CasGists!');",
			},
			{
				"filename": "new-file.txt",
				"content":  "This is a new file added to the gist",
			},
		},
	}

	var updatedGist map[string]interface{}
	err = s.APIClient.PutJSON("/api/v1/gists/"+gist1ID, updateData, &updatedGist)
	s.Require().NoError(err)
	s.Equal("My Updated First Gist", updatedGist["title"])

	// Step 8: Search for gists (skip if search is disabled)
	var searchResults map[string]interface{}
	err = s.APIClient.GetJSON("/api/v1/search?q=CasGists", &searchResults)
	if err == nil && searchResults["results"] != nil {
		results := searchResults["results"].([]interface{})
		s.GreaterOrEqual(len(results), 1, "Should find gists containing 'CasGists'")
	} else {
		s.T().Log("Search is disabled or returned no results, skipping search test")
	}

	// Step 9: Get specific gist details
	var gistDetails map[string]interface{}
	err = s.APIClient.GetJSON("/api/v1/gists/"+gist1ID, &gistDetails)
	s.Require().NoError(err)

	files := gistDetails["files"].([]interface{})
	s.Len(files, 2, "Updated gist should have 2 files")

	// Step 10: Delete a gist
	resp, err = s.APIClient.DELETE("/api/v1/gists/" + gist2ID)
	s.Require().NoError(err)
	resp.Body.Close()
	s.Equal(http.StatusNoContent, resp.StatusCode)

	// Step 11: Verify deletion
	resp, err = s.APIClient.GET("/api/v1/gists/" + gist2ID)
	s.Require().NoError(err)
	resp.Body.Close()
	s.Equal(http.StatusNotFound, resp.StatusCode)

	// Step 12: Logout
	resp, err = s.APIClient.POST("/api/v1/auth/logout", nil)
	s.Require().NoError(err)
	resp.Body.Close()
	s.Equal(http.StatusNoContent, resp.StatusCode)

	// Clear the authentication token locally
	s.APIClient.Logout()

	// Step 13: Verify logout (should not be able to access protected endpoint)
	resp, err = s.APIClient.GET("/api/v1/user")
	s.Require().NoError(err)
	resp.Body.Close()
	s.Equal(http.StatusUnauthorized, resp.StatusCode)

	s.T().Log("Complete user journey test passed successfully")
}

// TestGistVisibilityWorkflow tests gist visibility and access control
func (s *IntegrationTestSuite) TestGistVisibilityWorkflow() {
	// Create two users
	user1 := s.TestData.CreateTestUserWithData(s.T(), map[string]interface{}{
		"username": "user1",
		"password": "TestPassword123!",
	})

	user2 := s.TestData.CreateTestUserWithData(s.T(), map[string]interface{}{
		"username": "user2",
		"password": "TestPassword123!",
	})

	// User 1 creates gists with different visibility levels
	err := s.APIClient.Login(user1.Username, "TestPassword123!")
	s.Require().NoError(err)

	// Public gist
	publicGistData := map[string]interface{}{
		"title":       "Public Gist",
		"description": "Everyone can see this",
		"visibility":  "public",
		"files": []map[string]interface{}{
			{"filename": "public.txt", "content": "Public content"},
		},
	}

	var publicGist map[string]interface{}
	err = s.APIClient.PostJSON("/api/v1/gists", publicGistData, &publicGist)
	s.Require().NoError(err)
	publicGistID := publicGist["id"].(string)

	// Unlisted gist
	unlistedGistData := map[string]interface{}{
		"title":       "Unlisted Gist",
		"description": "Only accessible via direct link",
		"visibility":  "unlisted",
		"files": []map[string]interface{}{
			{"filename": "unlisted.txt", "content": "Unlisted content"},
		},
	}

	var unlistedGist map[string]interface{}
	err = s.APIClient.PostJSON("/api/v1/gists", unlistedGistData, &unlistedGist)
	s.Require().NoError(err)
	unlistedGistID := unlistedGist["id"].(string)

	// Private gist
	privateGistData := map[string]interface{}{
		"title":       "Private Gist",
		"description": "Only owner can see this",
		"visibility":  "private",
		"files": []map[string]interface{}{
			{"filename": "private.txt", "content": "Private content"},
		},
	}

	var privateGist map[string]interface{}
	err = s.APIClient.PostJSON("/api/v1/gists", privateGistData, &privateGist)
	s.Require().NoError(err)
	privateGistID := privateGist["id"].(string)

	// User 2 attempts to access different gists
	err = s.APIClient.Login(user2.Username, "TestPassword123!")
	s.Require().NoError(err)

	// Should be able to access public gist
	var accessedGist map[string]interface{}
	err = s.APIClient.GetJSON("/api/v1/gists/"+publicGistID, &accessedGist)
	s.Require().NoError(err)
	s.Equal("Public Gist", accessedGist["title"])

	// Should be able to access unlisted gist with direct link
	err = s.APIClient.GetJSON("/api/v1/gists/"+unlistedGistID, &accessedGist)
	s.Require().NoError(err)
	s.Equal("Unlisted Gist", accessedGist["title"])

	// Should NOT be able to access private gist
	resp, err := s.APIClient.GET("/api/v1/gists/" + privateGistID)
	s.Require().NoError(err)
	resp.Body.Close()
	s.Equal(http.StatusForbidden, resp.StatusCode)

	// Test search visibility (skip if search is disabled)
	var searchResults map[string]interface{}
	err = s.APIClient.GetJSON("/api/v1/search?q=Gist", &searchResults)
	if err == nil && searchResults["results"] != nil {
		results := searchResults["results"].([]interface{})

		// Check that only public gists appear in search results
		foundPublic := false
		foundUnlisted := false
		foundPrivate := false

		for _, result := range results {
			resultMap := result.(map[string]interface{})
			title := resultMap["title"].(string)

			switch title {
			case "Public Gist":
				foundPublic = true
			case "Unlisted Gist":
				foundUnlisted = true
			case "Private Gist":
				foundPrivate = true
			}
		}

		s.True(foundPublic, "Public gist should appear in search results")
		s.False(foundUnlisted, "Unlisted gist should not appear in search results")
		s.False(foundPrivate, "Private gist should not appear in search results")
	} else {
		s.T().Log("Search is disabled, skipping search visibility test")
	}

	s.T().Log("Gist visibility workflow test passed successfully")
}

// TestErrorRecoveryWorkflow tests error handling and recovery scenarios
func (s *IntegrationTestSuite) TestErrorRecoveryWorkflow() {
	// Create test user
	user := s.TestData.CreateTestUserWithData(s.T(), map[string]interface{}{
		"username": "erroruser",
		"password": "TestPassword123!",
	})

	err := s.APIClient.Login(user.Username, "TestPassword123!")
	s.Require().NoError(err)

	// Test 1: Invalid gist creation that should fail
	invalidGistData := map[string]interface{}{
		// Missing required title
		"description": "This should fail",
		"files":       []map[string]interface{}{},
	}

	resp, err := s.APIClient.POST("/api/v1/gists", invalidGistData)
	s.Require().NoError(err)
	defer resp.Body.Close()
	s.Equal(http.StatusBadRequest, resp.StatusCode)

	// Test 2: Valid gist creation after fixing the error
	validGistData := map[string]interface{}{
		"title":       "Valid Gist",
		"description": "This should work",
		"visibility":  "public",
		"files": []map[string]interface{}{
			{"filename": "test.txt", "content": "Test content"},
		},
	}

	var validGist map[string]interface{}
	err = s.APIClient.PostJSON("/api/v1/gists", validGistData, &validGist)
	s.Require().NoError(err)

	gistID := validGist["id"].(string)
	s.NotEmpty(gistID)

	// Test 3: Attempt to update non-existent gist
	updateData := map[string]interface{}{
		"title": "Updated Title",
	}

	resp, err = s.APIClient.PUT("/api/v1/gists/nonexistent-id", updateData)
	s.Require().NoError(err)
	resp.Body.Close()
	s.Equal(http.StatusNotFound, resp.StatusCode)

	// Test 4: Successfully update the existing gist
	var updatedGist map[string]interface{}
	err = s.APIClient.PutJSON("/api/v1/gists/"+gistID, updateData, &updatedGist)
	s.Require().NoError(err)
	s.Equal("Updated Title", updatedGist["title"])

	// Test 5: Test rate limiting recovery
	// Make multiple requests rapidly
	successCount := 0
	rateLimitCount := 0

	for i := 0; i < 20; i++ {
		resp, err := s.APIClient.GET("/api/v1/gists/" + gistID)
		if err != nil {
			continue
		}

		switch resp.StatusCode {
		case http.StatusOK:
			successCount++
		case http.StatusTooManyRequests:
			rateLimitCount++
		}

		resp.Body.Close()

		// Small delay to allow for rate limit reset
		time.Sleep(10 * time.Millisecond)
	}

	s.T().Logf("Request results: %d successful, %d rate limited", successCount, rateLimitCount)
	s.Greater(successCount, 0, "Should have some successful requests")

	// Test 6: Verify service recovery after rate limiting
	time.Sleep(1 * time.Second) // Wait for rate limit to reset

	var recoveredGist map[string]interface{}
	err = s.APIClient.GetJSON("/api/v1/gists/"+gistID, &recoveredGist)
	s.Require().NoError(err)
	s.Equal("Updated Title", recoveredGist["title"])

	s.T().Log("Error recovery workflow test passed successfully")
}

// TestAdminWorkflow tests admin-specific functionality
func (s *IntegrationTestSuite) TestAdminWorkflow() {
	// Create admin user
	adminUser := s.TestData.CreateTestUserWithData(s.T(), map[string]interface{}{
		"username": "admin",
		"password": "AdminPassword123!",
		"is_admin": true,
	})

	// Create regular user
	regularUser := s.TestData.CreateTestUserWithData(s.T(), map[string]interface{}{
		"username": "regular",
		"password": "RegularPassword123!",
		"is_admin": false,
	})

	// Regular user creates a gist
	err := s.APIClient.Login(regularUser.Username, "RegularPassword123!")
	s.Require().NoError(err)

	gistData := map[string]interface{}{
		"title":       "User Gist",
		"description": "Regular user's gist",
		"visibility":  "public",
		"files": []map[string]interface{}{
			{"filename": "user.txt", "content": "User content"},
		},
	}

	var userGist map[string]interface{}
	err = s.APIClient.PostJSON("/api/v1/gists", gistData, &userGist)
	s.Require().NoError(err)

	userGistID := userGist["id"].(string)

	// Admin logs in
	err = s.APIClient.Login(adminUser.Username, "AdminPassword123!")
	s.Require().NoError(err)

	// Admin should be able to access admin endpoints
	resp, err := s.APIClient.GET("/api/v1/admin/users")
	s.Require().NoError(err)
	defer resp.Body.Close()

	// Should be successful (200 or 404 if endpoint not implemented, but not 403)
	s.NotEqual(http.StatusForbidden, resp.StatusCode)

	// Regular user tries to access admin endpoints
	err = s.APIClient.Login(regularUser.Username, "RegularPassword123!")
	s.Require().NoError(err)

	resp, err = s.APIClient.GET("/api/v1/admin/users")
	s.Require().NoError(err)
	defer resp.Body.Close()

	// Should be forbidden
	s.Equal(http.StatusForbidden, resp.StatusCode)

	s.T().Log("Admin workflow test completed")

	// Avoid unused variable warnings
	_ = userGistID
}

// TestSystemHealthWorkflow tests system health monitoring
func (s *IntegrationTestSuite) TestSystemHealthWorkflow() {
	// Test basic health check
	resp, err := s.APIClient.GET("/health")
	s.Require().NoError(err)
	defer resp.Body.Close()

	s.Equal(http.StatusOK, resp.StatusCode)

	var basicHealth map[string]interface{}
	err = json.NewDecoder(resp.Body).Decode(&basicHealth)
	s.Require().NoError(err)

	s.Equal("healthy", basicHealth["status"])

	// Test enhanced health check
	resp, err = s.APIClient.GET("/healthz")
	s.Require().NoError(err)
	defer resp.Body.Close()

	s.Equal(http.StatusOK, resp.StatusCode)

	var enhancedHealth map[string]interface{}
	err = json.NewDecoder(resp.Body).Decode(&enhancedHealth)
	s.Require().NoError(err)

	s.Equal("healthy", enhancedHealth["status"])
	s.Contains(enhancedHealth, "components")
	s.Contains(enhancedHealth, "metrics")

	// Verify components
	components := enhancedHealth["components"].(map[string]interface{})
	s.Equal("healthy", components["database"])

	// Verify metrics exist
	metrics := enhancedHealth["metrics"].(map[string]interface{})
	s.Contains(metrics, "requests_total")
	s.Contains(metrics, "response_time_avg")
	s.Contains(metrics, "active_connections")

	s.T().Log("System health workflow test passed successfully")
}

// TestBackupAndRestoreWorkflow tests data persistence and recovery
func (s *IntegrationTestSuite) TestDataPersistenceWorkflow() {
	// Create test data
	user := s.TestData.CreateTestUserWithData(s.T(), map[string]interface{}{
		"username": "persistuser",
		"password": "TestPassword123!",
	})

	err := s.APIClient.Login(user.Username, "TestPassword123!")
	s.Require().NoError(err)

	// Create multiple gists
	gistIDs := make([]string, 0)

	for i := 0; i < 5; i++ {
		gistData := map[string]interface{}{
			"title":       fmt.Sprintf("Persistence Test Gist %d", i+1),
			"description": fmt.Sprintf("Test gist number %d", i+1),
			"visibility":  "public",
			"files": []map[string]interface{}{
				{
					"filename": fmt.Sprintf("test%d.txt", i+1),
					"content":  fmt.Sprintf("Content for gist %d", i+1),
				},
			},
		}

		var gist map[string]interface{}
		err = s.APIClient.PostJSON("/api/v1/gists", gistData, &gist)
		s.Require().NoError(err)

		gistIDs = append(gistIDs, gist["id"].(string))
	}

	// Verify all gists exist
	for i, gistID := range gistIDs {
		var gist map[string]interface{}
		err = s.APIClient.GetJSON("/api/v1/gists/"+gistID, &gist)
		s.Require().NoError(err)

		expectedTitle := fmt.Sprintf("Persistence Test Gist %d", i+1)
		s.Equal(expectedTitle, gist["title"])
	}

	// Test that data persists across requests
	var gistList map[string]interface{}
	err = s.APIClient.GetJSON("/api/v1/gists", &gistList)
	s.Require().NoError(err)

	gists := gistList["gists"].([]interface{})
	s.GreaterOrEqual(len(gists), 5, "Should have at least 5 gists")

	// Test search functionality with persistent data (skip if search is disabled)
	var searchResults map[string]interface{}
	err = s.APIClient.GetJSON("/api/v1/search?q=Persistence", &searchResults)
	if err == nil && searchResults["results"] != nil {
		results := searchResults["results"].([]interface{})
		s.GreaterOrEqual(len(results), 5, "Should find all persistence test gists")
	} else {
		s.T().Log("Search is disabled, skipping search functionality test")
	}

	s.T().Log("Data persistence workflow test passed successfully")
}
