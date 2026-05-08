package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/hallelujah-shih/ssh-multihop/internal/db"
	"github.com/hallelujah-shih/ssh-multihop/internal/service"
	"github.com/hallelujah-shih/ssh-multihop/internal/util"
	"go.uber.org/zap"
)

// Handlers handles HTTP requests
type Handlers struct {
	service *service.ForwardService
	db      *db.Database
}

// New creates a new Handlers instance
func New(svc *service.ForwardService, database *db.Database) *Handlers {
	return &Handlers{
		service: svc,
		db:      database,
	}
}

// ErrorResponse represents a standard error response
type ErrorResponse struct {
	Error   string `json:"error"`
	Code    string `json:"code,omitempty"`
	Details string `json:"details,omitempty"`
}

// CreateForwardRequest represents the request to create a forward
type CreateForwardRequest struct {
	Type        db.ForwardType `json:"type" binding:"required,oneof=local_listen_to_remote remote_listen_to_local remote_listen_to_remote"`
	ListenHost  string         `json:"listen_host" binding:"required"`
	ServiceHost string         `json:"service_host" binding:"required"`
	ListenAddr  string         `json:"listen_addr" binding:"required"`
	ServiceAddr string         `json:"service_addr" binding:"required"`
	MaxConns    int            `json:"max_conns" binding:"omitempty,min=0"`
	Description string         `json:"description" binding:"omitempty,max=500"`
	Sync        bool           `json:"sync,omitempty"` // If true, wait for forward to become active
}

const (
	// syncTimeout is the maximum time to wait for a forward to become active
	syncTimeout = 30 * time.Second
	// syncPollInterval is how often to check the forward status
	syncPollInterval = 500 * time.Millisecond
)

// waitForActiveStatus polls the database until the forward reaches "active" status
// or times out after 30 seconds. Returns the final status (which may be "running" or "error").
func waitForActiveStatus(db *db.Database, forwardID string) (*db.ForwardStatus, error) {
	ctx, cancel := context.WithTimeout(context.Background(), syncTimeout)
	defer cancel()

	ticker := time.NewTicker(syncPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("timeout waiting for forward to become active")
		case <-ticker.C:
			status, err := db.GetStatus(forwardID)
			if err != nil {
				// If no status yet, keep waiting
				continue
			}
			// Check for final statuses
			if status.Status == "running" {
				return status, nil
			}
			if status.Status == "error" {
				return status, nil
			}
		}
	}
}

// CreateForward handles POST /api/v1/forwards
func (h *Handlers) CreateForward(c *gin.Context) {
	var req CreateForwardRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		zap.L().Warn("Invalid create forward request", zap.Error(err))
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error:   "Invalid request",
			Details: err.Error(),
		})
		return
	}

	// Parse and validate addresses
	_, _, err := util.ParseAddress(req.ListenAddr)
	if err != nil {
		zap.L().Warn("Invalid listen_addr", zap.String("listen_addr", req.ListenAddr), zap.Error(err))
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error:   "Invalid listen_addr",
			Details: err.Error(),
			Code:    "INVALID_LISTEN_ADDR",
		})
		return
	}

	_, _, err = util.ParseAddress(req.ServiceAddr)
	if err != nil {
		zap.L().Warn("Invalid service_addr", zap.String("service_addr", req.ServiceAddr), zap.Error(err))
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error:   "Invalid service_addr",
			Details: err.Error(),
			Code:    "INVALID_SERVICE_ADDR",
		})
		return
	}

	// Type-specific validation
	if err := validateForwardType(&req); err != nil {
		zap.L().Warn("Invalid forward type configuration", zap.Error(err))
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error:   err.Error(),
			Details: err.Error(),
			Code:    "INVALID_CONFIGURATION",
		})
		return
	}

	// Create forward
	forward := &db.Forward{
		Type:        req.Type,
		ListenHost:  req.ListenHost,
		ServiceHost: req.ServiceHost,
		ListenAddr:  req.ListenAddr,
		ServiceAddr: req.ServiceAddr,
		MaxConns:    req.MaxConns,
		Description: req.Description,
	}

	if err := h.service.CreateForward(forward); err != nil {
		zap.L().Error("Failed to create forward",
			zap.String("type", string(req.Type)),
			zap.String("listen_host", req.ListenHost),
			zap.String("listen_addr", req.ListenAddr),
			zap.String("service_host", req.ServiceHost),
			zap.String("service_addr", req.ServiceAddr),
			zap.Error(err))
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error:   "Failed to create forward",
			Details: err.Error(),
			Code:    "CREATE_FAILED",
		})
		return
	}

	// If sync=true, wait for forward to become active
	if req.Sync {
		zap.L().Info("Sync mode enabled, waiting for forward to become active",
			zap.String("id", forward.ID))

		status, err := waitForActiveStatus(h.db, forward.ID)
		if err != nil {
			// Timeout waiting for active status
			zap.L().Warn("Timeout waiting for forward to become active",
				zap.String("id", forward.ID),
				zap.Error(err))
			c.JSON(http.StatusAccepted, gin.H{
				"id":            forward.ID,
				"status":        "pending",
				"error_message": "Timeout waiting for forward to become active",
			})
			return
		}

		// Return the final status
		zap.L().Info("Forward status determined",
			zap.String("id", forward.ID),
			zap.String("status", status.Status))

		responseStatus := http.StatusCreated
		if status.Status == "error" {
			responseStatus = http.StatusAccepted
		}

		c.JSON(responseStatus, gin.H{
			"id":            forward.ID,
			"type":          forward.Type,
			"listen_host":   forward.ListenHost,
			"service_host":  forward.ServiceHost,
			"listen_addr":   forward.ListenAddr,
			"service_addr":  forward.ServiceAddr,
			"max_conns":     forward.MaxConns,
			"description":   forward.Description,
			"created_at":    forward.CreatedAt,
			"updated_at":    forward.UpdatedAt,
			"status":        status.Status,
			"error_message": status.ErrorMessage,
		})
		return
	}

	zap.L().Info("Forward created",
		zap.String("id", forward.ID),
		zap.String("type", string(forward.Type)))
	c.JSON(http.StatusCreated, forward)
}

// validateForwardType validates the forward type configuration
func validateForwardType(req *CreateForwardRequest) error {
	switch req.Type {
	case db.LocalListenToRemote:
		if req.ListenHost != "local" {
			return fmt.Errorf("local_listen_to_remote must have listen_host='local'")
		}
		if req.ServiceHost == "local" {
			return fmt.Errorf("local_listen_to_remote cannot have service_host='local'")
		}

	case db.RemoteListenToLocal:
		if req.ListenHost == "local" {
			return fmt.Errorf("remote_listen_to_local cannot have listen_host='local'")
		}
		if req.ServiceHost != "local" {
			return fmt.Errorf("remote_listen_to_local must have service_host='local'")
		}

	case db.RemoteListenToRemote:
		if req.ListenHost == "local" {
			return fmt.Errorf("remote_listen_to_remote cannot have listen_host='local'")
		}
		if req.ServiceHost == "local" {
			return fmt.Errorf("remote_listen_to_remote cannot have service_host='local'")
		}

	default:
		return fmt.Errorf("unknown forward type: %s", req.Type)
	}

	return nil
}

// ListForwards handles GET /api/v1/forwards
func (h *Handlers) ListForwards(c *gin.Context) {
	forwards, err := h.service.ListForwards()
	if err != nil {
		zap.L().Error("Failed to list forwards", zap.Error(err))
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error:   "Failed to list forwards",
			Details: err.Error(),
			Code:    "LIST_FAILED",
		})
		return
	}

	c.JSON(http.StatusOK, forwards)
}

// GetForward handles GET /api/v1/forwards/:id
func (h *Handlers) GetForward(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error: "Invalid forward ID",
			Code:  "INVALID_ID",
		})
		return
	}

	forward, err := h.service.GetForward(id)
	if err != nil {
		if errors.Is(err, service.ErrForwardNotFound) {
			c.JSON(http.StatusNotFound, ErrorResponse{
				Error:   "Forward not found",
				Details: fmt.Sprintf("forward with id '%s' does not exist", id),
				Code:    "NOT_FOUND",
			})
			return
		}

		zap.L().Error("Failed to get forward", zap.String("id", id), zap.Error(err))
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error:   "Failed to get forward",
			Details: err.Error(),
			Code:    "GET_FAILED",
		})
		return
	}

	c.JSON(http.StatusOK, forward)
}

// DeleteForward handles DELETE /api/v1/forwards/:id
func (h *Handlers) DeleteForward(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error: "Invalid forward ID",
			Code:  "INVALID_ID",
		})
		return
	}

	if err := h.service.DeleteForward(id); err != nil {
		if errors.Is(err, service.ErrForwardNotFound) {
			c.JSON(http.StatusNotFound, ErrorResponse{
				Error:   "Forward not found",
				Details: fmt.Sprintf("forward with id '%s' does not exist", id),
				Code:    "NOT_FOUND",
			})
			return
		}

		zap.L().Error("Failed to delete forward", zap.String("id", id), zap.Error(err))
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error:   "Failed to delete forward",
			Details: err.Error(),
			Code:    "DELETE_FAILED",
		})
		return
	}

	zap.L().Info("Forward deleted", zap.String("id", id))
	c.JSON(http.StatusOK, gin.H{
		"message": "forward deleted",
		"id":      id,
	})
}

// ListStatuses handles GET /api/v1/status
func (h *Handlers) ListStatuses(c *gin.Context) {
	statuses, err := h.service.ListStatuses()
	if err != nil {
		zap.L().Error("Failed to list statuses", zap.Error(err))
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error:   "Failed to list statuses",
			Details: err.Error(),
			Code:    "LIST_FAILED",
		})
		return
	}

	c.JSON(http.StatusOK, statuses)
}

// GetStatus handles GET /api/v1/status/:id
func (h *Handlers) GetStatus(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error: "Invalid forward ID",
			Code:  "INVALID_ID",
		})
		return
	}

	status, err := h.service.GetStatus(id)
	if err != nil {
		if errors.Is(err, service.ErrStatusNotFound) {
			c.JSON(http.StatusNotFound, ErrorResponse{
				Error:   "Status not found",
				Details: fmt.Sprintf("status for forward '%s' does not exist", id),
				Code:    "NOT_FOUND",
			})
			return
		}

		zap.L().Error("Failed to get status", zap.String("id", id), zap.Error(err))
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error:   "Failed to get status",
			Details: err.Error(),
			Code:    "GET_FAILED",
		})
		return
	}

	c.JSON(http.StatusOK, status)
}

// GetPoolStats handles GET /pool/stats
func (h *Handlers) GetPoolStats(c *gin.Context) {
	stats, err := h.service.GetPoolStats()
	if err != nil {
		zap.L().Error("Failed to get pool stats", zap.Error(err))
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error:   "Failed to get pool stats",
			Details: err.Error(),
			Code:    "GET_POOL_STATS_FAILED",
		})
		return
	}

	c.JSON(http.StatusOK, stats)
}

// HealthCheck handles GET /health
func (h *Handlers) HealthCheck(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status": "healthy",
	})
}
