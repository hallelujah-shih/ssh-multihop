package api

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/hallelujah-shih/ssh-multihop/internal/db"
	"github.com/hallelujah-shih/ssh-multihop/internal/service"
	"go.uber.org/zap"
)

// Server represents the HTTP API server
type Server struct {
	httpServer *http.Server
	service    *service.ForwardService
	db         *db.Database
}

// Config holds server configuration
type Config struct {
	Host string
	Port int
}

// NewServer creates a new API server
func NewServer(cfg Config, svc *service.ForwardService, database *db.Database) *Server {
	// Set Gin to release mode
	gin.SetMode(gin.ReleaseMode)

	// Create router
	router := gin.Default()

	// Create handlers
	handlers := New(svc, database)

	// Register routes
	v1 := router.Group("/api/v1")
	{
		// Forwards
		v1.POST("/forwards", handlers.CreateForward)
		v1.GET("/forwards", handlers.ListForwards)
		v1.GET("/forwards/:id", handlers.GetForward)
		v1.DELETE("/forwards/:id", handlers.DeleteForward)

		// Status
		v1.GET("/status", handlers.ListStatuses)
		v1.GET("/status/:id", handlers.GetStatus)

		// Pool
		v1.GET("/pool/stats", handlers.GetPoolStats)
	}

	// Health check
	router.GET("/health", handlers.HealthCheck)

	// Create HTTP server
	httpServer := &http.Server{
		Addr:    net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port)),
		Handler: router,
	}

	return &Server{
		httpServer: httpServer,
		service:    svc,
		db:         database,
	}
}

// Start starts the API server
func (s *Server) Start(ctx context.Context) error {
	zap.L().Info("Starting API server",
		zap.String("addr", s.httpServer.Addr))

	// Start server in goroutine
	errCh := make(chan error, 1)
	go func() {
		if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	// Wait for context cancellation or server error
	select {
	case <-ctx.Done():
		zap.L().Info("Shutting down API server")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.httpServer.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("failed to shutdown server: %w", err)
		}
		return nil
	case err := <-errCh:
		return fmt.Errorf("server error: %w", err)
	}
}
