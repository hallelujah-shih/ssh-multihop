package agent

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"syscall"

	"github.com/hallelujah-shih/ssh-multihop/internal/util"
	"go.uber.org/zap"
)

// UserContext represents the user context for setuid/setgid scenarios.
// It uses the real UID (RUID) to determine the actual user, not the effective UID.
type UserContext struct {
	UID      int
	GID      int
	HomeDir  string
	Username string
}

// NewUserContext creates a new user context based on the real user ID.
// In setuid/setgid scenarios, this returns the actual user who executed the program,
// not the owner of the binary.
func NewUserContext() (*UserContext, error) {
	// Get real UID (not effective UID)
	uid := os.Getuid()
	gid := os.Getgid()

	// Get user info using util.UserHomeDir which queries passwd database by RUID
	homeDir, err := util.UserHomeDir()
	if err != nil || homeDir == "" {
		// Fallback to environment variable (may be wrong in setuidgid)
		homeDir = os.Getenv("HOME")
		if homeDir == "" {
			return nil, fmt.Errorf("failed to determine home directory: %w", err)
		}
	}

	// Try to get username from passwd database
	var username string
	if u, err := user.LookupId(fmt.Sprintf("%d", uid)); err == nil {
		username = u.Username
	} else {
		// Fallback: try to get from environment or current user
		if u, err := user.Current(); err == nil {
			username = u.Username
		} else {
			username = fmt.Sprintf("uid_%d", uid)
		}
	}

	uc := &UserContext{
		UID:      uid,
		GID:      gid,
		HomeDir:  homeDir,
		Username: username,
	}

	zap.L().Debug("Created user context",
		zap.Int("uid", uid),
		zap.Int("gid", gid),
		zap.String("username", username),
		zap.String("home_dir", homeDir))

	return uc, nil
}

// GetSSHConfigPath returns the path to the SSH config file.
func (uc *UserContext) GetSSHConfigPath() string {
	return filepath.Join(uc.HomeDir, ".ssh", "config")
}

// GetSSHDir returns the path to the SSH directory.
func (uc *UserContext) GetSSHDir() string {
	return filepath.Join(uc.HomeDir, ".ssh")
}

// GetAgentSocketPath returns the path to the agent socket file.
// The socket is placed in a temporary directory for better portability.
// Priority: XDG_RUNTIME_DIR (if set) > /tmp > $HOME/.ssh-multihop/agent
func (uc *UserContext) GetAgentSocketPath() string {
	// Try XDG_RUNTIME_DIR first (systemd sets this, guarantees per-user isolation)
	if runtimeDir := os.Getenv("XDG_RUNTIME_DIR"); runtimeDir != "" {
		agentDir := filepath.Join(runtimeDir, "ssh-multihop")
		pid := os.Getpid()
		return filepath.Join(agentDir, fmt.Sprintf("agent.%d.sock", pid))
	}

	// Fallback to system temp directory
	agentDir := filepath.Join(os.TempDir(), fmt.Sprintf("ssh-multihop-%d", uc.UID))
	pid := os.Getpid()
	return filepath.Join(agentDir, fmt.Sprintf("agent.%d.sock", pid))
}

// GetAgentDir returns the directory where agent sockets are stored.
func (uc *UserContext) GetAgentDir() string {
	// Try XDG_RUNTIME_DIR first (systemd sets this, guarantees per-user isolation)
	if runtimeDir := os.Getenv("XDG_RUNTIME_DIR"); runtimeDir != "" {
		return filepath.Join(runtimeDir, "ssh-multihop")
	}

	// Fallback to system temp directory
	return filepath.Join(os.TempDir(), fmt.Sprintf("ssh-multihop-%d", uc.UID))
}

// EnsureAgentDir creates the agent directory if it doesn't exist.
func (uc *UserContext) EnsureAgentDir() error {
	agentDir := uc.GetAgentDir()
	if err := os.MkdirAll(agentDir, 0700); err != nil {
		return fmt.Errorf("failed to create agent directory %s: %w", agentDir, err)
	}
	zap.L().Debug("Agent directory ensured",
		zap.String("dir", agentDir),
		zap.Int("uid", uc.UID))
	return nil
}

// String returns a string representation of the user context.
func (uc *UserContext) String() string {
	return fmt.Sprintf("%s (uid=%d, gid=%d)", uc.Username, uc.UID, uc.GID)
}

// IsSetUID returns true if running in setuid/setgid mode.
func (uc *UserContext) IsSetUID() bool {
	euid := os.Geteuid()
	egid := os.Getegid()
	return uc.UID != euid || uc.GID != egid
}

// GetEffectiveUser returns the effective user (owner of the binary).
func (uc *UserContext) GetEffectiveUser() string {
	euid := os.Geteuid()
	if u, err := user.LookupId(fmt.Sprintf("%d", euid)); err == nil {
		return u.Username
	}
	return fmt.Sprintf("euid_%d", euid)
}

// DropPrivileges temporarily drops privileges to the real user.
// This is useful for operations that need to run as the real user.
// Returns a function to restore privileges.
func (uc *UserContext) DropPrivileges() (restore func(), err error) {
	if !uc.IsSetUID() {
		// Not running in setuid mode, no need to drop
		return func() {}, nil
	}

	euid := os.Geteuid()
	egid := os.Getegid()

	// Drop to real user
	if err := syscall.Setegid(uc.GID); err != nil {
		return nil, fmt.Errorf("failed to drop group privileges: %w", err)
	}
	if err := syscall.Seteuid(uc.UID); err != nil {
		// Restore group before returning
		_ = syscall.Setegid(egid)
		return nil, fmt.Errorf("failed to drop user privileges: %w", err)
	}

	// Return restore function
	restore = func() {
		_ = syscall.Seteuid(euid)
		_ = syscall.Setegid(egid)
	}

	return restore, nil
}
