package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/hallelujah-shih/ssh-multihop/internal/agent"
	"github.com/hallelujah-shih/ssh-multihop/internal/api"
	"github.com/hallelujah-shih/ssh-multihop/internal/connection"
	"github.com/hallelujah-shih/ssh-multihop/internal/db"
	"github.com/hallelujah-shih/ssh-multihop/internal/service"
	"github.com/hallelujah-shih/ssh-multihop/internal/util"
	"github.com/urfave/cli/v2"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

func init() {
	logger, err := zap.NewDevelopment()
	if err != nil {
		// If we can't create a logger, we can't use zap for logging.
		// Fall back to stderr + exit.
		fmt.Fprintf(os.Stderr, "fatal error: failed to initialize logger: %v\n", err)
		os.Exit(1)
	}
	zap.ReplaceGlobals(logger)
}

// configureLogger reconfigures the global logger based on the specified log level.
func configureLogger(level string) error {
	// Map string level to zap.AtomicLevel
	var zapLevel zap.AtomicLevel
	switch level {
	case "debug":
		zapLevel = zap.NewAtomicLevelAt(zap.DebugLevel)
	case "info":
		zapLevel = zap.NewAtomicLevelAt(zap.InfoLevel)
	case "warn", "warning":
		zapLevel = zap.NewAtomicLevelAt(zap.WarnLevel)
	case "error":
		zapLevel = zap.NewAtomicLevelAt(zap.ErrorLevel)
	case "fatal":
		zapLevel = zap.NewAtomicLevelAt(zap.FatalLevel)
	default:
		return fmt.Errorf("invalid log level: %s (must be debug, info, warn, error, or fatal)", level)
	}

	// Create encoder config with development settings for better readability
	encoderConfig := zap.NewDevelopmentEncoderConfig()
	encoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
	encoderConfig.EncodeLevel = zapcore.CapitalLevelEncoder

	// Build logger with the specified level
	logger := zap.New(
		zapcore.NewCore(
			zapcore.NewConsoleEncoder(encoderConfig),
			zapcore.AddSync(os.Stdout),
			zapLevel,
		),
	)

	// Replace global logger
	zap.ReplaceGlobals(logger)

	return nil
}

var daemonCommand = &cli.Command{
	Name:  "daemon",
	Usage: "Start the SSH multi-hop forwarding daemon",
	Flags: []cli.Flag{
		&cli.StringFlag{
			Name:  "db",
			Usage: "Path to SQLite database (default: ~/.ssh-multihop/ssh-multihop-fwd.db)",
		},
		&cli.StringFlag{
			Name:  "host",
			Usage: "API server host",
			Value: "127.0.0.1",
		},
		&cli.IntFlag{
			Name:  "port",
			Usage: "API server port",
			Value: 8080,
		},
		&cli.StringFlag{
			Name:  "log-level",
			Usage: "Log level (debug, info, warn, error, fatal)",
			Value: "info",
		},
		&cli.StringFlag{
			Name:  "passphrase-socket",
			Usage: "Path to Unix socket for receiving SSH key passphrases (default: $XDG_RUNTIME_DIR/ssh-multihop/passphrase.sock or /tmp/ssh-multihop-<uid>/passphrase.sock)",
		},
	},
	Action: func(c *cli.Context) error {
		return runDaemon(c)
	},
}

func runDaemon(c *cli.Context) error {
	// Configure logger based on --log-level flag
	logLevel := c.String("log-level")
	if err := configureLogger(logLevel); err != nil {
		return fmt.Errorf("failed to configure logger: %w", err)
	}

	// Get configuration
	dbPath := c.String("db")
	host := c.String("host")
	port := c.Int("port")
	passphraseSocketPath := c.String("passphrase-socket")

	// Set default db path if not specified
	// Use util.UserHomeDir() for setuid/setgid compatibility
	if dbPath == "" {
		homeDir, err := util.UserHomeDir()
		if err != nil || homeDir == "" {
			return fmt.Errorf("failed to determine home directory: %w", err)
		}
		dbPath = filepath.Join(homeDir, ".ssh-multihop", "ssh-multihop-fwd.db")
	}

	// Set default passphrase socket path if not specified
	if passphraseSocketPath == "" {
		uid := os.Getuid()
		if runtimeDir := os.Getenv("XDG_RUNTIME_DIR"); runtimeDir != "" {
			passphraseSocketPath = filepath.Join(runtimeDir, "ssh-multihop", "passphrase.sock")
		} else {
			passphraseSocketPath = filepath.Join(os.TempDir(), fmt.Sprintf("ssh-multihop-%d", uid), "passphrase.sock")
		}
	}

	// Ensure database directory exists
	dbDir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dbDir, 0755); err != nil {
		return fmt.Errorf("failed to create database directory: %w", err)
	}

	// Ensure passphrase socket directory exists
	socketDir := filepath.Dir(passphraseSocketPath)
	if err := os.MkdirAll(socketDir, 0700); err != nil {
		return fmt.Errorf("failed to create socket directory: %w", err)
	}

	zap.L().Info("Starting SSH multi-hop forwarding daemon",
		zap.String("db_path", dbPath),
		zap.String("api_addr", net.JoinHostPort(host, strconv.Itoa(port))),
		zap.String("passphrase_socket", passphraseSocketPath))

	// Debug: Log SSH_AUTH_SOCK environment variable
	sshAuthSock := os.Getenv("SSH_AUTH_SOCK")
	zap.L().Debug("SSH_AUTH_SOCK environment variable",
		zap.String("SSH_AUTH_SOCK", sshAuthSock),
		zap.Int("uid", os.Getuid()),
		zap.Int("euid", os.Geteuid()),
		zap.Int("gid", os.Getgid()),
		zap.Int("egid", os.Getegid()))

	// Check for existing SSH agent before initializing built-in agent
	externalSocket, keyCount, err := agent.CheckExternalAgent()

	zap.L().Debug("External SSH agent check result",
		zap.String("socket", externalSocket),
		zap.Int("keys", keyCount),
		zap.Error(err))

	if err == nil && externalSocket != "" && keyCount > 0 {
		// Found a working external agent (e.g., GNOME Keyring, ssh-agent)
		zap.L().Info("Using existing SSH agent",
			zap.String("socket", externalSocket),
			zap.Int("keys", keyCount))
	} else {
		// No external agent available, initialize built-in agent
		// Clean up any orphaned ssh-agent processes from previous crashes
		if err := agent.CleanupOrphanedAgents(); err != nil {
			zap.L().Warn("Failed to clean up orphaned SSH agents",
				zap.Error(err))
		}

		// Initialize built-in SSH agent early, before any SSH connections
		if _, err := agent.GetBuiltInAgent(); err != nil {
			zap.L().Warn("Failed to initialize built-in SSH agent",
				zap.Error(err))
			// Continue anyway - might be able to use external agent or keys
		}
	}

	// Start passphrase socket if configured
	var passphraseSocket *connection.PassphraseSocket
	if passphraseSocketPath != "" {
		passphraseSocket = connection.NewPassphraseSocket(passphraseSocketPath)
		if err := passphraseSocket.Start(); err != nil {
			return fmt.Errorf("failed to start passphrase socket: %w", err)
		}
		defer passphraseSocket.Stop()
		zap.L().Info("Passphrase socket started", zap.String("socket", passphraseSocketPath))
	}
	// Initialize database
	database, err := db.New(db.Config{Path: dbPath})
	if err != nil {
		return fmt.Errorf("failed to initialize database: %w", err)
	}
	defer func() {
		if err := database.Close(); err != nil {
			zap.L().Error("Failed to close database", zap.Error(err))
		}
	}()

	// Check if API server port is already in use BEFORE starting any forwards
	apiAddr := net.JoinHostPort(host, strconv.Itoa(port))
	conn, err := net.DialTimeout("tcp", apiAddr, 100*time.Millisecond)
	if err == nil {
		_ = conn.Close()
		return fmt.Errorf("API server port %s is already in use by another process", apiAddr)
	}

	zap.L().Info("API server port is available", zap.String("addr", apiAddr))

	// Create root context for all components
	// This is the single source of truth for shutdown
	rootCtx, rootCancel := context.WithCancel(context.Background())
	defer rootCancel() // Ensure all derived contexts are cancelled

	// Initialize service with root context
	svc, err := service.NewWithContext(rootCtx, database)
	if err != nil {
		return fmt.Errorf("failed to create service: %w", err)
	}

	// Set passphrase socket if available
	if passphraseSocket != nil {
		svc.SetPassphraseSocket(passphraseSocket)
	}

	// Start service (load and start all forwards)
	if err := svc.Start(); err != nil {
		// Cleanup service if start fails
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer stopCancel()
		_ = svc.StopWithContext(stopCtx)
		return fmt.Errorf("failed to start service: %w", err)
	}

	// Initialize API server
	server := api.NewServer(api.Config{
		Host: host,
		Port: port,
	}, svc, database)

	// Setup signal handling
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	// Start server in goroutine
	errCh := make(chan error, 1)
	go func() {
		errCh <- server.Start(rootCtx)
	}()

	// Wait for signal or error
	select {
	case sig := <-sigCh:
		zap.L().Info("Received signal, initiating graceful shutdown", zap.String("signal", sig.String()))

		// Step 1: Cancel root context (stops all components)
		rootCancel()

		// Step 2: Wait for API server to shutdown (5s timeout)
		zap.L().Info("Waiting for API server shutdown")
		serverDone := make(chan error, 1)
		go func() {
			serverDone <- <-errCh
		}()

		select {
		case err := <-serverDone:
			if err != nil {
				zap.L().Warn("Server shutdown with error", zap.Error(err))
			} else {
				zap.L().Info("API server shutdown complete")
			}
		case <-time.After(5 * time.Second):
			zap.L().Warn("Server shutdown timeout after 5s")
		}

		// Step 3: Stop all forwards with timeout (10s)
		zap.L().Info("Stopping all forwards")
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer stopCancel()

		if err := svc.StopWithContext(stopCtx); err != nil {
			zap.L().Warn("Service stop completed with errors", zap.Error(err))
		} else {
			zap.L().Info("All forwards stopped successfully")
		}

		// Step 4: Clean up built-in SSH agent
		if agent, agentErr := agent.GetBuiltInAgent(); agentErr == nil {
			if stopErr := agent.Stop(); stopErr != nil {
				zap.L().Warn("Failed to stop built-in SSH agent",
					zap.Error(stopErr))
			} else {
				zap.L().Info("Built-in SSH agent stopped successfully")
			}
		}

		zap.L().Info("Graceful shutdown complete")
		return nil

	case err := <-errCh:
		if err != nil {
			zap.L().Error("Server error", zap.Error(err))
			// Still try to cleanup on error
			rootCancel() // Cancel all contexts

			stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer stopCancel()
			_ = svc.StopWithContext(stopCtx)

			if agent, agentErr := agent.GetBuiltInAgent(); agentErr == nil {
				_ = agent.Stop()
			}
			return err
		}
	}

	return nil
}
