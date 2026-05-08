package forwarding

import (
	"context"
)

// ForwardStatus represents the current status of a forward
type ForwardStatus int

const (
	// StatusStopped means the forward is not running
	StatusStopped ForwardStatus = iota
	// StatusError means the forward encountered an error and needs to be rebuilt
	StatusError
	// StatusRunning means the forward is actively running
	StatusRunning
)

// String returns the string representation of ForwardStatus
func (fs ForwardStatus) String() string {
	switch fs {
	case StatusStopped:
		return "stopped"
	case StatusError:
		return "error"
	case StatusRunning:
		return "running"
	default:
		return "unknown"
	}
}

// Forward represents a port forwarding configuration
//
// Forward is self-contained: it manages its own connections,
// listeners, health checks, and data forwarding. Once started,
// it runs independently until stopped.
type Forward interface {
	// Start begins the port forwarding
	// This method blocks until the forward is stopped or an error occurs
	Start(ctx context.Context) error

	// Stop gracefully stops the port forwarding
	// This closes all connections and listeners
	Stop() error

	// HealthCheck performs an active health check
	// Returns an error if the forward is not healthy
	HealthCheck() error

	// Type returns the forward type as string (e.g., "local_listen_to_remote")
	Type() string

	// Status returns the current status
	Status() ForwardStatus

	// String returns a string representation (for debugging)
	String() string

	// SetPassphraseSocket sets the passphrase socket for retrieving SSH key passphrases
	SetPassphraseSocket(ps interface{})
}
