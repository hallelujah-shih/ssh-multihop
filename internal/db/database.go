package db

import (
	"fmt"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Database handles all database operations
type Database struct {
	db *gorm.DB
}

// Config holds database configuration
type Config struct {
	Path string // Path to SQLite database file
}

// New creates a new database instance
func New(cfg Config) (*Database, error) {
	// Open database (GORM will auto-create parent directories)
	db, err := gorm.Open(sqlite.Open(cfg.Path), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// Auto migrate schema
	if err := db.AutoMigrate(&Forward{}, &ForwardStatus{}); err != nil {
		return nil, fmt.Errorf("failed to migrate database: %w", err)
	}

	return &Database{db: db}, nil
}

// Close closes the database connection
func (d *Database) Close() error {
	sqlDB, err := d.db.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}

// CreateForward creates a new forward rule
func (d *Database) CreateForward(forward *Forward) error {
	return d.db.Create(forward).Error
}

// GetForward retrieves a forward by ID
func (d *Database) GetForward(id string) (*Forward, error) {
	var forward Forward
	err := d.db.Where("id = ?", id).First(&forward).Error
	if err != nil {
		return nil, err
	}
	return &forward, nil
}

// ListForwards lists all forwards
func (d *Database) ListForwards() ([]Forward, error) {
	var forwards []Forward
	err := d.db.Find(&forwards).Error
	return forwards, err
}

// UpdateForward updates a forward
func (d *Database) UpdateForward(forward *Forward) error {
	return d.db.Save(forward).Error
}

// DeleteForward deletes a forward using hard delete
// Deprecated: Use DeleteForwardAndStatus for atomic deletion with status
func (d *Database) DeleteForward(id string) error {
	return d.db.Unscoped().Delete(&Forward{}, "id = ?", id).Error
}

// CreateOrUpdateStatus creates or updates forward status
func (d *Database) CreateOrUpdateStatus(status *ForwardStatus) error {
	return d.db.Save(status).Error
}

// GetStatus retrieves the current status of a forward
func (d *Database) GetStatus(forwardID string) (*ForwardStatus, error) {
	var status ForwardStatus
	err := d.db.Where("forward_id = ?", forwardID).First(&status).Error
	if err != nil {
		return nil, err
	}
	return &status, nil
}

// ListStatuses lists all forward statuses
func (d *Database) ListStatuses() ([]ForwardStatus, error) {
	var statuses []ForwardStatus
	err := d.db.Find(&statuses).Error
	return statuses, err
}

// DeleteStatus deletes a forward status
func (d *Database) DeleteStatus(forwardID string) error {
	return d.db.Delete(&ForwardStatus{}, "forward_id = ?", forwardID).Error
}

// DeleteForwardAndStatus deletes both forward and its status in a transaction
// Uses hard delete (Unscoped) to allow recreating the same forward
func (d *Database) DeleteForwardAndStatus(id string) error {
	return d.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Unscoped().Delete(&Forward{}, "id = ?", id).Error; err != nil {
			return err
		}
		if err := tx.Delete(&ForwardStatus{}, "forward_id = ?", id).Error; err != nil {
			return err
		}
		return nil
	})
}

// CleanStatuses removes all status records
func (d *Database) CleanStatuses() error {
	return d.db.Exec("DELETE FROM forward_status").Error
}

// Transaction executes the given function in a database transaction
func (d *Database) Transaction(fn func(tx *gorm.DB) error) error {
	return d.db.Transaction(fn)
}

// ForwardWithStatus represents a forward with its status
// This is a separate result struct for JOIN queries (not modifying Forward model)
// We use embedded structs to avoid GORM relation confusion
type ForwardWithStatus struct {
	ForwardID   string         `gorm:"column:id" json:"id"`
	Type        string         `gorm:"column:type" json:"type"`
	ListenHost  string         `gorm:"column:listen_host" json:"listen_host"`
	ServiceHost string         `gorm:"column:service_host" json:"service_host"`
	ListenAddr  string         `gorm:"column:listen_addr" json:"listen_addr"`
	ServiceAddr string         `gorm:"column:service_addr" json:"service_addr"`
	MaxConns    int            `gorm:"column:max_conns" json:"max_conns"`
	Description string         `gorm:"column:description" json:"description"`
	CreatedAt   time.Time `gorm:"column:created_at" json:"created_at"`
	UpdatedAt   time.Time `gorm:"column:updated_at" json:"updated_at"`

	// Status fields (from forward_status table, may be null)
	Status        *string    `gorm:"column:status" json:"status,omitempty"`
	LastHeartbeat *time.Time `gorm:"column:last_heartbeat" json:"last_heartbeat,omitempty"`
	ErrorMessage  *string    `gorm:"column:error_message" json:"error_message,omitempty"`
}

// ToForward converts ForwardWithStatus to Forward model
func (fws *ForwardWithStatus) ToForward() Forward {
	return Forward{
		ID:          fws.ForwardID,
		Type:        ForwardType(fws.Type),
		ListenHost:  fws.ListenHost,
		ServiceHost: fws.ServiceHost,
		ListenAddr:  fws.ListenAddr,
		ServiceAddr: fws.ServiceAddr,
		MaxConns:    fws.MaxConns,
		Description: fws.Description,
		CreatedAt:   fws.CreatedAt,
		UpdatedAt:   fws.UpdatedAt,
	}
}

// ToStatus converts ForwardWithStatus to ForwardStatus model (if status exists)
func (fws *ForwardWithStatus) ToStatus() *ForwardStatus {
	if fws.Status == nil {
		return nil
	}

	var errorMessage string
	if fws.ErrorMessage != nil {
		errorMessage = *fws.ErrorMessage
	}

	return &ForwardStatus{
		ForwardID:     fws.ForwardID,
		Status:        *fws.Status,
		LastHeartbeat: *fws.LastHeartbeat,
		ErrorMessage:  errorMessage,
		// CreatedAt/UpdatedAt from status table are not included in the JOIN
	}
}

// ListForwardsWithStatus lists all forwards with their statuses using a single LEFT JOIN query
// This reduces database round-trips from O(N) to O(1) compared to calling GetStatus() for each forward
func (d *Database) ListForwardsWithStatus() ([]ForwardWithStatus, error) {
	var results []ForwardWithStatus

	query := `
		SELECT
			f.id,
			f.type,
			f.listen_host,
			f.service_host,
			f.listen_addr,
			f.service_addr,
			f.max_conns,
			f.description,
			f.created_at,
			f.updated_at,
			fs.status,
			fs.last_heartbeat,
			fs.error_message
		FROM forwards f
		LEFT JOIN forward_status fs ON f.id = fs.forward_id
	`

	err := d.db.Raw(query).Scan(&results).Error

	return results, err
}
