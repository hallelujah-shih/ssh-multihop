// Package connection provides SSH connection management for multi-hop tunnels.
//
// Core components:
//   - Establisher: establishes SSH connections through hop chains, with
//     timeouts on TCP dials, tunneled channel opens, and SSH handshakes
//   - ConnectionManager: pools connections keyed by signature, with reference
//     counting, hop reuse (ProxyJump), and linger-based idle recycling
//   - HealthChecker: monitors pooled connections and cancels the context of
//     dead connections so forwards can react
//   - SSHClientConfigBuilder: builds SSH client configs (keys, certificates,
//     ssh-agent, passphrase socket)
//   - MultiplexedForward: creates SSH channels over pooled connections
//   - PassphraseSocket: serves passphrases to key decryption over a UDS
//
// Reconnection of forwards is handled by the ForwardService layer; forwards
// themselves fail fast.
package connection
