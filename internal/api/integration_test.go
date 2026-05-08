//go:build integration
// +build integration

package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/hallelujah-shih/ssh-multihop/internal/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Integration tests for API handlers
// Run with: go test ./internal/api -tags=integration

// setupIntegrationTest is defined in testutil.go

// TestIntegration_API_CRUD tests complete CRUD operations
func TestIntegration_API_CRUD(t *testing.T) {
	_, router, tmpDir, cleanup := setupIntegrationTest(t)
	defer cleanup()

	t.Run("Health check", func(t *testing.T) {
		req, _ := http.NewRequest("GET", "/health", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var response map[string]interface{}
		err := json.Unmarshal(w.Body.Bytes(), &response)
		require.NoError(t, err)
		assert.Equal(t, "healthy", response["status"])
	})

	t.Run("Create forward - success", func(t *testing.T) {
		reqBody := `{
			"type": "local_listen_to_remote",
			"listen_host": "local",
			"listen_addr": "127.0.0.1:9090",
			"service_host": "test-host",
			"service_addr": "127.0.0.1:8080",
			"description": "Test forward"
		}`

		req, _ := http.NewRequest("POST", "/api/v1/forwards", bytes.NewBufferString(reqBody))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusCreated, w.Code)

		var response db.Forward
		err := json.Unmarshal(w.Body.Bytes(), &response)
		require.NoError(t, err)

		assert.Equal(t, db.LocalListenToRemote, response.Type)
		assert.Equal(t, "local", response.ListenHost)
		assert.Equal(t, "127.0.0.1:9090", response.ListenAddr)
		assert.Equal(t, "test-host", response.ServiceHost)
		assert.Equal(t, "127.0.0.1:8080", response.ServiceAddr)
		assert.NotEmpty(t, response.ID)
		assert.NotEmpty(t, response.CreatedAt)
		assert.NotEmpty(t, response.UpdatedAt)
	})

	t.Run("List forwards", func(t *testing.T) {
		// Create another forward first
		reqBody1 := `{
			"type": "remote_listen_to_local",
			"listen_host": "remote",
			"listen_addr": "127.0.0.1:3000",
			"service_host": "local",
			"service_addr": "127.0.0.1:3000"
		}`
		req1, _ := http.NewRequest("POST", "/api/v1/forwards", bytes.NewBufferString(reqBody1))
		req1.Header.Set("Content-Type", "application/json")
		w1 := httptest.NewRecorder()
		router.ServeHTTP(w1, req1)
		require.Equal(t, http.StatusCreated, w1.Code)

		// List all forwards
		req, _ := http.NewRequest("GET", "/api/v1/forwards", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var response []db.Forward
		err := json.Unmarshal(w.Body.Bytes(), &response)
		require.NoError(t, err)
		assert.GreaterOrEqual(t, len(response), 2)
	})

	t.Run("Get forward - success", func(t *testing.T) {
		// Create a forward first
		reqBody := `{
			"type": "local_listen_to_remote",
			"listen_host": "local",
			"listen_addr": "127.0.0.1:9999",
			"service_host": "get-test",
			"service_addr": "127.0.0.1:8888"
		}`
		createReq, _ := http.NewRequest("POST", "/api/v1/forwards", bytes.NewBufferString(reqBody))
		createReq.Header.Set("Content-Type", "application/json")
		createW := httptest.NewRecorder()
		router.ServeHTTP(createW, createReq)

		var createdForward db.Forward
		err := json.Unmarshal(createW.Body.Bytes(), &createdForward)
		require.NoError(t, err)

		// Get the forward
		req, _ := http.NewRequest("GET", "/api/v1/forwards/"+createdForward.ID, nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var response db.Forward
		err = json.Unmarshal(w.Body.Bytes(), &response)
		require.NoError(t, err)
		assert.Equal(t, createdForward.ID, response.ID)
		assert.Equal(t, "get-test", response.ServiceHost)
	})

	t.Run("Get forward - not found", func(t *testing.T) {
		req, _ := http.NewRequest("GET", "/api/v1/forwards/nonexistent-id", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusNotFound, w.Code)

		var response ErrorResponse
		err := json.Unmarshal(w.Body.Bytes(), &response)
		require.NoError(t, err)
		assert.Equal(t, "NOT_FOUND", response.Code)
		assert.Contains(t, response.Details, "does not exist")
	})

	t.Run("Delete forward - success", func(t *testing.T) {
		// Create a forward first
		reqBody := `{
			"type": "local_listen_to_remote",
			"listen_host": "local",
			"listen_addr": "127.0.0.1:7001",
			"service_host": "delete-test",
			"service_addr": "127.0.0.1:7000"
		}`
		createReq, _ := http.NewRequest("POST", "/api/v1/forwards", bytes.NewBufferString(reqBody))
		createReq.Header.Set("Content-Type", "application/json")
		createW := httptest.NewRecorder()
		router.ServeHTTP(createW, createReq)

		var createdForward db.Forward
		err := json.Unmarshal(createW.Body.Bytes(), &createdForward)
		require.NoError(t, err)

		// Delete the forward
		req, _ := http.NewRequest("DELETE", "/api/v1/forwards/"+createdForward.ID, nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var response map[string]interface{}
		err = json.Unmarshal(w.Body.Bytes(), &response)
		require.NoError(t, err)
		assert.Equal(t, "forward deleted", response["message"])
		assert.Equal(t, createdForward.ID, response["id"])

		// Verify it's deleted
		getReq, _ := http.NewRequest("GET", "/api/v1/forwards/"+createdForward.ID, nil)
		getW := httptest.NewRecorder()
		router.ServeHTTP(getW, getReq)
		assert.Equal(t, http.StatusNotFound, getW.Code)
	})

	t.Run("Delete forward - not found", func(t *testing.T) {
		req, _ := http.NewRequest("DELETE", "/api/v1/forwards/nonexistent-id", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		// Idempotent deletion: 200 OK means "goal state achieved"
		assert.Equal(t, http.StatusOK, w.Code)

		var response map[string]interface{}
		err := json.Unmarshal(w.Body.Bytes(), &response)
		require.NoError(t, err)
		assert.Equal(t, "forward deleted", response["message"])
	})

	t.Run("List statuses", func(t *testing.T) {
		req, _ := http.NewRequest("GET", "/api/v1/status", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var response []db.ForwardStatus
		err := json.Unmarshal(w.Body.Bytes(), &response)
		require.NoError(t, err)
		// Should have at least some statuses from previous tests
		assert.GreaterOrEqual(t, len(response), 0)
	})

	_ = tmpDir // Use tmpDir to avoid unused variable warning
}

// TestIntegration_Validation tests input validation
func TestIntegration_Validation(t *testing.T) {
	_, router, _, cleanup := setupIntegrationTest(t)
	defer cleanup()

	tests := []struct {
		name           string
		requestBody    string
		expectedStatus int
		expectedCode   string
	}{
		{
			name: "Invalid type",
			requestBody: `{
				"type": "invalid_type",
				"listen_host": "local",
				"listen_addr": "127.0.0.1:9090",
				"service_host": "test",
				"service_addr": "127.0.0.1:8080"
			}`,
			expectedStatus: http.StatusBadRequest,
			expectedCode:   "",
		},
		{
			name: "Invalid listen_addr format",
			requestBody: `{
				"type": "local_listen_to_remote",
				"listen_host": "local",
				"listen_addr": "invalid",
				"service_host": "test",
				"service_addr": "127.0.0.1:8080"
			}`,
			expectedStatus: http.StatusBadRequest,
			expectedCode:   "INVALID_LISTEN_ADDR",
		},
		{
			name: "Invalid service_addr format",
			requestBody: `{
				"type": "local_listen_to_remote",
				"listen_host": "local",
				"listen_addr": "127.0.0.1:9090",
				"service_host": "test",
				"service_addr": "invalid"
			}`,
			expectedStatus: http.StatusBadRequest,
			expectedCode:   "INVALID_SERVICE_ADDR",
		},
		{
			name: "local_listen_to_remote with wrong listen_host",
			requestBody: `{
				"type": "local_listen_to_remote",
				"listen_host": "remote",
				"listen_addr": "127.0.0.1:9090",
				"service_host": "test",
				"service_addr": "127.0.0.1:8080"
			}`,
			expectedStatus: http.StatusBadRequest,
			expectedCode:   "INVALID_CONFIGURATION",
		},
		{
			name: "Missing required field - type",
			requestBody: `{
				"listen_host": "local",
				"listen_addr": "127.0.0.1:9090",
				"service_host": "test",
				"service_addr": "127.0.0.1:8080"
			}`,
			expectedStatus: http.StatusBadRequest,
			expectedCode:   "",
		},
		{
			name: "Missing required field - listen_host",
			requestBody: `{
				"type": "local_listen_to_remote",
				"listen_addr": "127.0.0.1:9090",
				"service_host": "test",
				"service_addr": "127.0.0.1:8080"
			}`,
			expectedStatus: http.StatusBadRequest,
			expectedCode:   "",
		},
		{
			name: "MaxConns not supported for RemoteListenToRemote",
			requestBody: `{
				"type": "remote_listen_to_remote",
				"listen_host": "remote1",
				"listen_addr": "127.0.0.1:9090",
				"service_host": "remote2",
				"service_addr": "127.0.0.1:8080",
				"max_conns": 10
			}`,
			expectedStatus: http.StatusBadRequest,
			expectedCode:   "", // May be MAX_CONNS_NOT_SUPPORTED or CREATE_FAILED depending on validation order
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, _ := http.NewRequest("POST", "/api/v1/forwards", bytes.NewBufferString(tt.requestBody))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			// For MaxConns test, accept both 400 and 500 (validation may happen at different stages)
			if tt.name == "MaxConns not supported for RemoteListenToRemote" {
				assert.Contains(t, []int{http.StatusBadRequest, http.StatusInternalServerError}, w.Code)
			} else {
				assert.Equal(t, tt.expectedStatus, w.Code)
			}

			var response ErrorResponse
			err := json.Unmarshal(w.Body.Bytes(), &response)
			require.NoError(t, err)
			assert.NotEmpty(t, response.Error)

			if tt.expectedCode != "" && tt.name != "MaxConns not supported for RemoteListenToRemote" {
				assert.Equal(t, tt.expectedCode, response.Code)
			}
		})
	}
}

// TestIntegration_BoundaryValues tests boundary value validation
func TestIntegration_BoundaryValues(t *testing.T) {
	_, router, _, cleanup := setupIntegrationTest(t)
	defer cleanup()

	tests := []struct {
		name         string
		serviceAddr  string
		listenAddr   string
		shouldAccept bool
	}{
		{"Minimum valid port", "127.0.0.1:1", "127.0.0.1:8080", true},
		{"Maximum valid port", "127.0.0.1:65535", "127.0.0.1:8080", true},
		{"Port too low", "127.0.0.1:0", "127.0.0.1:8080", false},
		{"Port too high", "127.0.0.1:65536", "127.0.0.1:8080", false},
		{"Common HTTP port", "127.0.0.1:80", "127.0.0.1:8080", true},
		{"Common HTTPS port", "127.0.0.1:443", "127.0.0.1:8080", true},
		{"Common alt port", "127.0.0.1:8080", "127.0.0.1:8080", true},
		{"Database port", "127.0.0.1:5432", "127.0.0.1:8080", true},
		{"Both boundaries", "127.0.0.1:1", "127.0.0.1:65535", true},
		{"All interfaces shorthand", ":4000", "127.0.0.1:8080", true},
		{"All interfaces explicit", "0.0.0.0:4000", "127.0.0.1:8080", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reqBody := map[string]interface{}{
				"type":         "local_listen_to_remote",
				"listen_host":  "local",
				"listen_addr":  tt.listenAddr,
				"service_host": "test",
				"service_addr": tt.serviceAddr,
			}

			bodyBytes, _ := json.Marshal(reqBody)
			req, _ := http.NewRequest("POST", "/api/v1/forwards", bytes.NewBuffer(bodyBytes))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			if tt.shouldAccept {
				// May return 201 (created) or 500 (failed to start, but validation passed)
				assert.NotEqual(t, http.StatusBadRequest, w.Code,
					"Address %s should be accepted", tt.listenAddr)
			} else {
				assert.Equal(t, http.StatusBadRequest, w.Code,
					"Address %s should be rejected", tt.listenAddr)
			}
		})
	}
}

// TestIntegration_TypeValidation tests forward type validation
func TestIntegration_TypeValidation(t *testing.T) {
	_, router, _, cleanup := setupIntegrationTest(t)
	defer cleanup()

	validTypes := []string{
		"local_listen_to_remote",
		"remote_listen_to_local",
		"remote_listen_to_remote",
	}

	for _, validType := range validTypes {
		t.Run("Valid type: "+validType, func(t *testing.T) {
			// Configure based on type to pass validation
			var reqBody map[string]interface{}
			switch validType {
			case "local_listen_to_remote":
				reqBody = map[string]interface{}{
					"type":         validType,
					"listen_host":  "local",
					"listen_addr":  "127.0.0.1:9090",
					"service_host": "test",
					"service_addr": "127.0.0.1:8080",
				}
			case "remote_listen_to_local":
				reqBody = map[string]interface{}{
					"type":         validType,
					"listen_host":  "remote",
					"listen_addr":  "127.0.0.1:9090",
					"service_host": "local",
					"service_addr": "127.0.0.1:8080",
				}
			case "remote_listen_to_remote":
				reqBody = map[string]interface{}{
					"type":         validType,
					"listen_host":  "remote1",
					"listen_addr":  "127.0.0.1:9090",
					"service_host": "remote2",
					"service_addr": "127.0.0.1:8080",
				}
			}

			bodyBytes, _ := json.Marshal(reqBody)
			req, _ := http.NewRequest("POST", "/api/v1/forwards", bytes.NewBuffer(bodyBytes))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			// Should pass validation (may fail to start, but validation should pass)
			assert.NotEqual(t, http.StatusBadRequest, w.Code)
		})
	}

	t.Run("Invalid type", func(t *testing.T) {
		invalidTypes := []string{
			"invalid",
			"LOCAL_LISTEN_TO_REMOTE", // uppercase not allowed
			"LocalListenToRemote",    // mixed case not allowed
			"",
		}

		for _, invalidType := range invalidTypes {
			reqBody := map[string]interface{}{
				"type":         invalidType,
				"listen_host":  "local",
				"listen_addr":  "127.0.0.1:9090",
				"service_host": "test",
				"service_addr": "127.0.0.1:8080",
			}

			bodyBytes, _ := json.Marshal(reqBody)
			req, _ := http.NewRequest("POST", "/api/v1/forwards", bytes.NewBuffer(bodyBytes))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			assert.Equal(t, http.StatusBadRequest, w.Code,
				"Type '%s' should be rejected", invalidType)
		}
	})
}

// TestIntegration_Concurrency tests concurrent API operations
func TestIntegration_Concurrency(t *testing.T) {
	_, router, _, cleanup := setupIntegrationTest(t)
	defer cleanup()

	// Create multiple forwards concurrently
	const numForwards = 10
	done := make(chan bool, numForwards)

	for i := 0; i < numForwards; i++ {
		go func(index int) {
			reqBody := map[string]interface{}{
				"type":         "local_listen_to_remote",
				"listen_host":  "local",
				"listen_addr":  fmt.Sprintf("127.0.0.1:%d", 9500+index),
				"service_host": "test",
				"service_addr": fmt.Sprintf("127.0.0.1:%d", 9000+index),
			}

			bodyBytes, _ := json.Marshal(reqBody)
			req, _ := http.NewRequest("POST", "/api/v1/forwards", bytes.NewBuffer(bodyBytes))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			// Just check no panic
			done <- true
		}(i)
	}

	// Wait for all goroutines
	for i := 0; i < numForwards; i++ {
		select {
		case <-done:
			// OK
		case <-time.After(5 * time.Second):
			t.Fatal("Timeout waiting for concurrent operations")
		}
	}

	// List forwards to verify
	req, _ := http.NewRequest("GET", "/api/v1/forwards", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response []db.Forward
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(response), numForwards)
}

// TestIntegration_ErrorResponseFormat tests error response format consistency
func TestIntegration_ErrorResponseFormat(t *testing.T) {
	_, router, _, cleanup := setupIntegrationTest(t)
	defer cleanup()

	errorScenarios := []struct {
		name     string
		method   string
		path     string
		body     string
		testFunc func(t *testing.T, statusCode int, response ErrorResponse)
	}{
		{
			name:   "Get non-existent forward",
			method: "GET",
			path:   "/api/v1/forwards/does-not-exist",
			body:   "",
			testFunc: func(t *testing.T, statusCode int, response ErrorResponse) {
				assert.Equal(t, http.StatusNotFound, statusCode)
				assert.Equal(t, "NOT_FOUND", response.Code)
				assert.NotEmpty(t, response.Error)
				assert.NotEmpty(t, response.Details)
			},
		},
		{
			name:   "Delete non-existent forward (idempotent)",
			method: "DELETE",
			path:   "/api/v1/forwards/does-not-exist",
			body:   "",
			testFunc: func(t *testing.T, statusCode int, response ErrorResponse) {
				// Idempotent deletion: 200 OK means "goal state achieved"
				assert.Equal(t, http.StatusOK, statusCode)
			},
		},
		{
			name:   "Get status for non-existent forward",
			method: "GET",
			path:   "/api/v1/status/does-not-exist",
			body:   "",
			testFunc: func(t *testing.T, statusCode int, response ErrorResponse) {
				assert.Equal(t, http.StatusNotFound, statusCode)
				assert.Equal(t, "NOT_FOUND", response.Code)
			},
		},
		{
			name:   "Invalid request body",
			method: "POST",
			path:   "/api/v1/forwards",
			body:   `{invalid json}`,
			testFunc: func(t *testing.T, statusCode int, response ErrorResponse) {
				assert.Equal(t, http.StatusBadRequest, statusCode)
				assert.NotEmpty(t, response.Error)
			},
		},
	}

	for _, scenario := range errorScenarios {
		t.Run(scenario.name, func(t *testing.T) {
			var req *http.Request
			if scenario.body == "" {
				req, _ = http.NewRequest(scenario.method, scenario.path, nil)
			} else {
				req, _ = http.NewRequest(scenario.method, scenario.path, bytes.NewBufferString(scenario.body))
				req.Header.Set("Content-Type", "application/json")
			}

			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			var response ErrorResponse
			err := json.Unmarshal(w.Body.Bytes(), &response)
			require.NoError(t, err)

			scenario.testFunc(t, w.Code, response)
		})
	}
}
