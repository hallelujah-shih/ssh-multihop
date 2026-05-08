package api

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/hallelujah-shih/ssh-multihop/internal/db"
	"github.com/hallelujah-shih/ssh-multihop/internal/service"
	"github.com/stretchr/testify/require"
)

// createTestService creates a ForwardService with an in-memory database for testing
//
//nolint:unused // Used by integration tests with build tag
func createTestService(t *testing.T) (*service.ForwardService, *db.Database, func()) {
	// Create temporary database
	tmpDir, err := os.MkdirTemp("", "api-test-*")
	require.NoError(t, err)
	dbPath := filepath.Join(tmpDir, "test.db")

	// Initialize database
	database, err := db.New(db.Config{Path: dbPath})
	require.NoError(t, err)

	// Initialize service
	svc, err := service.NewWithContext(context.Background(), database)
	require.NoError(t, err)

	// Start service
	err = svc.Start()
	require.NoError(t, err)

	// Cleanup function
	cleanup := func() {
		_ = svc.Stop()
		_ = database.Close()
		_ = os.RemoveAll(tmpDir)
	}

	return svc, database, cleanup
}

// createTestServer creates a test HTTP server with Gin router and API handlers
//
//nolint:unused // Used by integration tests with build tag
func createTestServer(t *testing.T, svc *service.ForwardService, database *db.Database) *gin.Engine {
	// Create router
	gin.SetMode(gin.TestMode)
	router := gin.New()
	handlers := New(svc, database)

	// Register all routes
	router.POST("/api/v1/forwards", handlers.CreateForward)
	router.GET("/api/v1/forwards", handlers.ListForwards)
	router.GET("/api/v1/forwards/:id", handlers.GetForward)
	router.DELETE("/api/v1/forwards/:id", handlers.DeleteForward)
	router.GET("/api/v1/status", handlers.ListStatuses)
	router.GET("/api/v1/status/:id", handlers.GetStatus)
	router.GET("/health", handlers.HealthCheck)

	return router
}

// setupIntegrationTest creates a complete test environment with service and server
// Returns: service, router, tmpDir path, and cleanup function
//
//nolint:unused // Used by integration tests with build tag
func setupIntegrationTest(t *testing.T) (*service.ForwardService, *gin.Engine, string, func()) {
	svc, database, cleanup := createTestService(t)
	router := createTestServer(t, svc, database)

	// Get tmpDir path for potential additional cleanup
	tmpDir := filepath.Dir(os.TempDir() + "/api-test-*")

	return svc, router, tmpDir, cleanup
}
