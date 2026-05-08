package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/hallelujah-shih/ssh-multihop/internal/db"
	"github.com/hallelujah-shih/ssh-multihop/internal/service"
	"github.com/stretchr/testify/require"
)

// TestCreateForwardAsync tests sync=false (default behavior)
func TestCreateForwardAsync(t *testing.T) {
	// Setup
	database, err := db.New(db.Config{Path: ":memory:"})
	if err != nil {
		t.Fatalf("Failed to create database: %v", err)
	}
	defer func() {
		if err := database.Close(); err != nil {
			t.Logf("Failed to close database: %v", err)
		}
	}()

	svc, err := service.NewWithContext(context.Background(), database)
	require.NoError(t, err)
	handlers := New(svc, database)

	// Create request
	reqBody := CreateForwardRequest{
		Type:        db.LocalListenToRemote,
		ListenHost:  "local",
		ServiceHost: "remote",
		ListenAddr:  ":8080",
		ServiceAddr: ":9090",
		Sync:        false,
	}
	body, _ := json.Marshal(reqBody)

	req, _ := http.NewRequest("POST", "/api/v1/forwards", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")

	// Record response
	rec := httptest.NewRecorder()
	c := testGinEngine(rec, req)

	// Execute
	handlers.CreateForward(c)

	// Assert
	if rec.Code != http.StatusCreated {
		t.Errorf("Expected status %d, got %d", http.StatusCreated, rec.Code)
	}

	var response map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}

	// Should return immediately with forward data
	if response["id"] == nil {
		t.Error("Expected forward ID in response")
	}

	// Status should not be in response for async mode
	if _, ok := response["status"]; ok {
		t.Error("Status should not be in response for async mode")
	}
}

// TestCreateForwardSyncTimeout tests sync=true with timeout scenario
func TestCreateForwardSyncTimeout(t *testing.T) {
	// Setup
	database, err := db.New(db.Config{Path: ":memory:"})
	if err != nil {
		t.Fatalf("Failed to create database: %v", err)
	}
	defer func() {
		if err := database.Close(); err != nil {
			t.Logf("Failed to close database: %v", err)
		}
	}()

	svc, err := service.NewWithContext(context.Background(), database)
	require.NoError(t, err)
	handlers := New(svc, database)

	// Create request with invalid forward (will never become active)
	reqBody := CreateForwardRequest{
		Type:        db.LocalListenToRemote,
		ListenHost:  "local",
		ServiceHost: "nonexistent-host-12345", // Invalid host
		ListenAddr:  ":8080",
		ServiceAddr: ":9090",
		Sync:        true,
	}
	body, _ := json.Marshal(reqBody)

	req, _ := http.NewRequest("POST", "/api/v1/forwards", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")

	// Record response with timeout
	timeout := make(chan bool, 1)
	go func() {
		time.Sleep(35 * time.Second) // Wait longer than syncTimeout (30s)
		timeout <- true
	}()

	rec := httptest.NewRecorder()
	c := testGinEngine(rec, req)

	done := make(chan bool, 1)
	go func() {
		handlers.CreateForward(c)
		done <- true
	}()

	// Wait for either completion or timeout
	select {
	case <-done:
		// Test completed within timeout
	case <-timeout:
		t.Fatal("Test timed out - sync mode did not timeout after 30s")
	}

	// Assert
	if rec.Code != http.StatusAccepted {
		t.Errorf("Expected status %d, got %d", http.StatusAccepted, rec.Code)
	}

	var response map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}

	// Should return pending status with timeout message
	if response["status"] != "pending" {
		t.Errorf("Expected status 'pending', got '%v'", response["status"])
	}

	if response["error_message"] == nil {
		t.Error("Expected error message in response")
	}
}

// TestCreateForwardSyncImmediateError tests sync=true with immediate error
func TestCreateForwardSyncImmediateError(t *testing.T) {
	// Setup
	database, err := db.New(db.Config{Path: ":memory:"})
	if err != nil {
		t.Fatalf("Failed to create database: %v", err)
	}
	defer func() {
		if err := database.Close(); err != nil {
			t.Logf("Failed to close database: %v", err)
		}
	}()

	svc, err := service.NewWithContext(context.Background(), database)
	require.NoError(t, err)
	handlers := New(svc, database)

	// Create request with invalid configuration (will fail validation)
	reqBody := CreateForwardRequest{
		Type:        db.LocalListenToRemote,
		ListenHost:  "remote", // Invalid: must be "local" for LocalListenToRemote
		ServiceHost: "remote",
		ListenAddr:  ":8080",
		ServiceAddr: ":9090",
		Sync:        true,
	}
	body, _ := json.Marshal(reqBody)

	req, _ := http.NewRequest("POST", "/api/v1/forwards", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")

	// Record response
	rec := httptest.NewRecorder()
	c := testGinEngine(rec, req)

	// Execute
	handlers.CreateForward(c)

	// Assert
	if rec.Code != http.StatusBadRequest {
		t.Errorf("Expected status %d, got %d", http.StatusBadRequest, rec.Code)
	}

	var response map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}

	// Should return error
	if response["error"] == nil {
		t.Error("Expected error in response")
	}
}

// testGinEngine creates a test Gin context
func testGinEngine(w *httptest.ResponseRecorder, r *http.Request) *gin.Context {
	gin.SetMode(gin.TestMode)
	router, _ := gin.CreateTestContext(w)
	c := router
	c.Request = r

	return c
}
