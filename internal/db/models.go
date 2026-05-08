package db

import (
	"fmt"
	"time"

	"gorm.io/gorm"
)

// Forward represents a port forwarding rule
type Forward struct {
	ID   string      `gorm:"primaryKey;size:64" json:"id"`
	Type ForwardType `gorm:"type:varchar(20);not null" json:"type"`

	// Host configuration (SSH hostnames)
	ListenHost  string `gorm:"size:255;not null" json:"listen_host"`
	ServiceHost string `gorm:"size:255;not null" json:"service_host"`

	// Address configuration (standard [ip]:port or :port format)
	ListenAddr  string `gorm:"size:50;not null" json:"listen_addr"`
	ServiceAddr string `gorm:"size:50;not null" json:"service_addr"`

	// Optional configuration
	MaxConns    int    `gorm:"default:0" json:"max_conns"`
	Description string `gorm:"size:500" json:"description"`

	// Timestamps
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`
}

// ForwardType represents the type of forwarding
type ForwardType string

const (
	// LocalListenToRemote: Listen on local, forward to remote (SSH -L)
	LocalListenToRemote ForwardType = "local_listen_to_remote"
	// RemoteListenToLocal: Listen on remote, forward to local (SSH -R)
	RemoteListenToLocal ForwardType = "remote_listen_to_local"
	// RemoteListenToRemote: Listen on remote A, forward to remote B
	RemoteListenToRemote ForwardType = "remote_listen_to_remote"
)

// ForwardStatus represents the current status of a forward
type ForwardStatus struct {
	ForwardID     string    `gorm:"primaryKey;size:64" json:"forward_id"`
	Status        string    `gorm:"type:varchar(20);not null" json:"status"`
	LastHeartbeat time.Time `json:"last_heartbeat"`
	ErrorMessage  string    `gorm:"type:text" json:"error_message,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// TableName specifies the table name for Forward
func (Forward) TableName() string {
	return "forwards"
}

// TableName specifies the table name for ForwardStatus
func (ForwardStatus) TableName() string {
	return "forward_status"
}

// BeforeCreate hook
func (f *Forward) BeforeCreate(tx *gorm.DB) error {
	if f.ID == "" {
		f.ID = generateForwardID(f.Type, f.ListenHost, f.ListenAddr, f.ServiceHost, f.ServiceAddr)
	}
	now := time.Now()
	f.CreatedAt = now
	f.UpdatedAt = now
	return nil
}

// BeforeUpdate hook
func (f *Forward) BeforeUpdate(tx *gorm.DB) error {
	f.UpdatedAt = time.Now()
	return nil
}

// generateForwardID generates a unique forward ID
func generateForwardID(fwdType ForwardType, listenHost string, listenAddr string, serviceHost string, serviceAddr string) string {
	return fmt.Sprintf("%s-%s-%s-%s-%s", fwdType, listenHost, listenAddr, serviceHost, serviceAddr)
}

// IsRunning returns true if the forward is in running state
func (fs *ForwardStatus) IsRunning() bool {
	return fs.Status == "running"
}

// DBNow returns current time for database operations
func DBNow() time.Time {
	return time.Now()
}
