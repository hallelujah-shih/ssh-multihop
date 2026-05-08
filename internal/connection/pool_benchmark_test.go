package connection_test

import (
	"context"
	"fmt"
	"net"
	"os"
	"testing"
	"time"

	"github.com/hallelujah-shih/ssh-multihop/internal/config"
	"github.com/hallelujah-shih/ssh-multihop/internal/connection"
	"github.com/hallelujah-shih/ssh-multihop/internal/db"
	"github.com/hallelujah-shih/ssh-multihop/internal/service"
	"github.com/hallelujah-shih/ssh-multihop/internal/tunnel"
)

// BenchmarkConnectionPoolCreation measures the time to create multiple forwards
// with connection pooling enabled.
func BenchmarkConnectionPoolCreation(b *testing.B) {
	// Skip if test host is not available
	if !isTestHostAvailable() {
		b.Skip("test host not available")
	}

	// Create temporary database
	tmpDB, err := createTempDB()
	if err != nil {
		b.Fatalf("Failed to create temp database: %v", err)
	}
	defer func() { _ = os.Remove(tmpDB) }()

	database, err := db.New(db.Config{Path: tmpDB})
	if err != nil {
		b.Fatalf("Failed to open database: %v", err)
	}
	defer func() { _ = database.Close() }()

	// Create ForwardService with connection pool
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	svc, err := service.NewWithContext(ctx, database)
	if err != nil {
		b.Fatalf("Failed to create service: %v", err)
	}
	defer func() { _ = svc.Stop() }()

	b.ResetTimer()

	// Benchmark creating 10 forwards
	for i := 0; i < b.N; i++ {
		// Create forwards to the same host (should share connection)
		for j := 0; j < 10; j++ {
			listenAddr := fmt.Sprintf("127.0.0.1:%d", 19000+i*10+j)
			createForwardViaService(b, svc, listenAddr, testHost, "127.0.0.1:22")
		}

		// Clean up all forwards
		cleanupAllForwards(b, svc)
	}
}

// BenchmarkNoPoolCreation measures the time to create multiple forwards
// without connection pooling (simulates old behavior).
func BenchmarkNoPoolCreation(b *testing.B) {
	// Skip if test host is not available
	if !isTestHostAvailable() {
		b.Skip("test host not available")
	}

	// Create temporary database
	tmpDB, err := createTempDB()
	if err != nil {
		b.Fatalf("Failed to create temp database: %v", err)
	}
	defer func() { _ = os.Remove(tmpDB) }()

	database, err := db.New(db.Config{Path: tmpDB})
	if err != nil {
		b.Fatalf("Failed to open database: %v", err)
	}
	defer func() { _ = database.Close() }()

	// Create ForwardService with connection pool
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	svc, err := service.NewWithContext(ctx, database)
	if err != nil {
		b.Fatalf("Failed to create service: %v", err)
	}
	defer func() { _ = svc.Stop() }()

	b.ResetTimer()

	// Benchmark creating 10 forwards to DIFFERENT hosts (forces new connections)
	for i := 0; i < b.N; i++ {
		// Create forwards to different hosts (cannot share connection)
		for j := 0; j < 10; j++ {
			listenAddr := fmt.Sprintf("127.0.0.1:%d", 19000+i*10+j)
			// Use different port to simulate different destinations
			serviceAddr := fmt.Sprintf("127.0.0.1:%d", 22+j)
			createForwardViaService(b, svc, listenAddr, testHost, serviceAddr)
		}

		// Clean up all forwards
		cleanupAllForwards(b, svc)
	}
}

// BenchmarkConcurrentAcquireRelease benchmarks concurrent acquire/release operations.
func BenchmarkConcurrentAcquireRelease(b *testing.B) {
	// Skip if test host is not available
	if !isTestHostAvailable() {
		b.Skip("test host not available")
	}

	// Create connection manager
	pool := connection.NewConnectionManager(
		connection.DefaultConfig(),
		createTestHopConfigProvider(),
	)
	defer func() { _ = pool.Close() }()

	sig := createTestSignature()

	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			// Acquire connection
			conn, err := pool.Acquire(context.Background(), sig, "benchmark-forward")
			if err != nil {
				b.Errorf("Failed to acquire connection: %v", err)
				continue
			}

			// Simulate some work
			time.Sleep(10 * time.Millisecond)

			// Release connection
			if err := pool.Release(conn, "benchmark-forward"); err != nil {
				b.Errorf("Failed to release connection: %v", err)
			}
		}
	})
}

// BenchmarkConnectionReuse benchmarks the time savings from reusing connections.
func BenchmarkConnectionReuse(b *testing.B) {
	// Skip if test host is not available
	if !isTestHostAvailable() {
		b.Skip("test host not available")
	}

	pool := connection.NewConnectionManager(
		connection.DefaultConfig(),
		createTestHopConfigProvider(),
	)
	defer func() { _ = pool.Close() }()

	sig := createTestSignature()

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		// First acquire (creates connection)
		conn1, err := pool.Acquire(context.Background(), sig, "forward-1")
		if err != nil {
			b.Fatalf("Failed to acquire first connection: %v", err)
		}

		// Second acquire (reuses connection)
		conn2, err := pool.Acquire(context.Background(), sig, "forward-2")
		if err != nil {
			b.Fatalf("Failed to acquire second connection: %v", err)
		}

		// Release both
		_ = pool.Release(conn1, "forward-1")
		_ = pool.Release(conn2, "forward-2")
	}
}

// BenchmarkMemoryUsage benchmarks memory usage with connection pooling.
func BenchmarkMemoryUsage(b *testing.B) {
	// Skip if test host is not available
	if !isTestHostAvailable() {
		b.Skip("test host not available")
	}

	pool := connection.NewConnectionManager(
		connection.DefaultConfig(),
		createTestHopConfigProvider(),
	)
	defer func() { _ = pool.Close() }()

	sig := createTestSignature()

	b.ReportAllocs()

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		// Create and release 100 connections
		for j := 0; j < 100; j++ {
			conn, err := pool.Acquire(context.Background(), sig, fmt.Sprintf("forward-%d", j))
			if err != nil {
				b.Fatalf("Failed to acquire connection: %v", err)
			}
			_ = pool.Release(conn, fmt.Sprintf("forward-%d", j))
		}
	}
}

// Helper functions

const testHost = "vmr.u24"

func isTestHostAvailable() bool {
	// Check if we can reach the test host
	conn, err := net.DialTimeout("tcp", testHost+":22", 2*time.Second)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func createTempDB() (string, error) {
	f, err := os.CreateTemp("", "ssh-multihop-benchmark-*.db")
	if err != nil {
		return "", err
	}
	_ = f.Close()
	return f.Name(), nil
}

func createForwardViaService(b *testing.B, svc *service.ForwardService, listenAddr, serviceHost, serviceAddr string) {
	forward := &db.Forward{
		Type:        "local_listen_to_remote",
		ListenHost:  "local",
		ListenAddr:  listenAddr,
		ServiceHost: serviceHost,
		ServiceAddr: serviceAddr,
		Description: "benchmark forward",
	}

	if err := svc.CreateForward(forward); err != nil {
		b.Errorf("Failed to create forward: %v", err)
	}
}

func cleanupAllForwards(b *testing.B, svc *service.ForwardService) {
	forwards, _ := svc.ListForwards()
	for _, forward := range forwards {
		_ = svc.DeleteForward(forward.ID)
	}
}

func createTestHopConfigProvider() connection.HopConfigProvider {
	return func(sig connection.ConnectionSignature) ([]*tunnel.HopConfig, *connection.SSHClientConfigBuilder, error) {
		// Parse SSH config
		parser := config.NewParser()
		hostConfig, err := parser.GetHostConfig(testHost)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to get host config: %w", err)
		}

		// Build hop config
		hop := &tunnel.HopConfig{
			Host:            testHost,
			HostName:        hostConfig.HostName,
			Port:            hostConfig.Port,
			User:            hostConfig.User,
			IdentityFile:    hostConfig.IdentityFile,
			CertificateFile: hostConfig.CertificateFile,
		}

		// Apply defaults
		if hop.HostName == "" {
			hop.HostName = testHost
		}
		if hop.Port == 0 {
			hop.Port = 22
		}

		// Create SSH client config builder
		builder := connection.NewSSHClientConfigBuilder()

		return []*tunnel.HopConfig{hop}, builder, nil
	}
}

func createTestSignature() connection.ConnectionSignature {
	return connection.ConnectionSignature{
		Username:  "test",
		Hostname:  testHost,
		Port:      22,
		JumpChain: []string{},
	}
}
