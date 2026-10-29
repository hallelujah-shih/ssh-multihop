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
	"github.com/hallelujah-shih/ssh-multihop/internal/daemoncfg"
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
			Name:  "config",
			Usage: "Path to daemon config YAML (default: ~/.ssh-multihop/config.yaml; missing default = built-in defaults)",
		},
		&cli.StringFlag{
			Name:  "env-file",
			Usage: "Environment file (KEY=VALUE) providing key passphrases",
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

	// Set default db path if not specified
	// Use util.UserHomeDir() for setuid/setgid compatibility
	if dbPath == "" {
		homeDir, err := util.UserHomeDir()
		if err != nil || homeDir == "" {
			return fmt.Errorf("failed to determine home directory: %w", err)
		}
		dbPath = filepath.Join(homeDir, ".ssh-multihop", "ssh-multihop-fwd.db")
	}

	configFlag := c.String("config")
	var cfg *daemoncfg.Config
	var err error
	if configFlag != "" {
		cfg, err = daemoncfg.Load(configFlag)
		if err != nil {
			return fmt.Errorf("failed to load config: %w", err)
		}
	} else {
		cfg, err = daemoncfg.LoadDefault()
		if err != nil {
			return fmt.Errorf("failed to load default config: %w", err)
		}
		configFlag, _ = daemoncfg.DefaultPath() // S1: 默认路径也实打日志（可能为空串, 仅日志用途）
	}
	envLoaded := 0
	if envFile := c.String("env-file"); envFile != "" {
		var envErr error
		envLoaded, envErr = daemoncfg.LoadEnvFile(envFile)
		if envErr != nil {
			return fmt.Errorf("failed to load env file: %w", envErr)
		}
	}
	if err := cfg.ResolvedKeyPaths(); err != nil {
		return fmt.Errorf("invalid key paths in config: %w", err)
	}
	sshConfigPath, err := cfg.ResolvedSSHConfigPath()
	if err != nil {
		return fmt.Errorf("invalid ssh_dir: %w", err)
	}
	zap.L().Info("Daemon configuration loaded",
		zap.String("config", configFlag),
		zap.Bool("explicit_keys", cfg.ExplicitKeys()),
		zap.Int("keys", len(cfg.Keys)),
		zap.Int("env_vars_loaded", envLoaded),
		zap.String("ssh_config", sshConfigPath))

	// Ensure database directory exists
	dbDir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dbDir, 0755); err != nil {
		return fmt.Errorf("failed to create database directory: %w", err)
	}

	zap.L().Info("Starting SSH multi-hop forwarding daemon",
		zap.String("db_path", dbPath),
		zap.String("api_addr", net.JoinHostPort(host, strconv.Itoa(port))))

	// Debug: Log SSH_AUTH_SOCK environment variable
	sshAuthSock := os.Getenv("SSH_AUTH_SOCK")
	zap.L().Debug("SSH_AUTH_SOCK environment variable",
		zap.String("SSH_AUTH_SOCK", sshAuthSock),
		zap.Int("uid", os.Getuid()),
		zap.Int("euid", os.Geteuid()),
		zap.Int("gid", os.Getgid()),
		zap.Int("egid", os.Getegid()))

	if err := initKeysForRun(cfg, sshConfigPath, func(opts agent.LoadOptions) error {
		_, err := agent.GetBuiltInAgent(opts)
		return err
	}); err != nil {
		return err
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
		if agent, agentErr := agent.GetBuiltInAgent(agent.LoadOptions{}); agentErr == nil {
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
			if err := svc.StopWithContext(stopCtx); err != nil {
				zap.L().Warn("Service stop completed with errors", zap.Error(err))
			}

			if agent, agentErr := agent.GetBuiltInAgent(agent.LoadOptions{}); agentErr == nil {
				_ = agent.Stop()
			}
			return err
		}
	}

	return nil
}

// initKeysForRun initializes key material per config mode.
// Explicit mode is fatal on any key failure; auto mode keeps today's lenient behavior.
// initAgent is injected: runDaemon passes the real GetBuiltInAgent closure; tests
// construct per-call loaders (process-wide sync.Once would make tests order-coupled).
func initKeysForRun(cfg *daemoncfg.Config, sshConfigPath string, initAgent func(agent.LoadOptions) error) error {
	externalSocket, keyCount, err := agent.CheckExternalAgent()
	if err != nil {
		zap.L().Warn("External SSH agent check failed", zap.Error(err))
	}
	if cfg.ExplicitKeys() {
		// Strict mode: configured keys must load regardless of any external agent.
		if err := initAgent(agent.LoadOptions{Keys: cfg.Keys, SSHConfigPath: sshConfigPath}); err != nil {
			return fmt.Errorf("failed to load configured SSH keys: %w", err)
		}
		if externalSocket != "" {
			zap.L().Info("Configured keys loaded; external agent preferred for authentication",
				zap.String("socket", externalSocket))
		}
		return nil
	}
	if err == nil && externalSocket != "" && keyCount > 0 {
		zap.L().Info("Using existing SSH agent",
			zap.String("socket", externalSocket),
			zap.Int("keys", keyCount))
		return nil
	}
	// CleanupOrphanedAgents 保留现状调用（pure Go agent 下为 no-op, 行为零变化）
	if err := agent.CleanupOrphanedAgents(); err != nil {
		zap.L().Warn("Failed to clean up orphaned SSH agents", zap.Error(err))
	}
	if err := initAgent(agent.LoadOptions{SSHConfigPath: sshConfigPath}); err != nil {
		zap.L().Warn("Failed to initialize built-in SSH agent", zap.Error(err))
	}
	return nil
}
