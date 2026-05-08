package tunnel

// HopConfig represents a single hop in the tunnel chain.
type HopConfig struct {
	// Host is the alias/pattern for this hop
	Host string
	// HostName is the actual hostname or IP address
	HostName string
	// Port is the SSH port number
	Port int
	// User is the username for SSH authentication
	User string
	// IdentityFile is the path to the SSH private key file
	IdentityFile string
	// CertificateFile is the path to the SSH certificate file
	CertificateFile string
}
