package connection

import "time"

const (
	// DefaultHealthCheckInterval is how often to check connection health
	DefaultHealthCheckInterval = 30 * time.Second

	// DefaultHealthCheckTimeout is timeout for individual health checks
	DefaultHealthCheckTimeout = 5 * time.Second

	// DefaultMaxRetries is maximum reconnection attempts
	DefaultMaxRetries = 10

	// DefaultMaxBackoff is maximum backoff delay between retries
	DefaultMaxBackoff = 30 * time.Second

	// DefaultInitialBackoff is initial backoff delay
	DefaultInitialBackoff = 1 * time.Second
)

const (
	// DefaultDialTimeout is timeout for establishing SSH connection
	DefaultDialTimeout = 30 * time.Second

	// DefaultKeepAliveInterval is SSH keep-alive interval
	DefaultKeepAliveInterval = 15 * time.Second

	// DefaultMaxHops is maximum number of hops in a chain
	DefaultMaxHops = 5
)
