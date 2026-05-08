package main

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"strings"
)

func main() {
	if len(os.Args) < 2 || len(os.Args) > 3 {
		fmt.Fprintf(os.Stderr, "Usage: %s <socket-path> [passphrase]\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "\nIf passphrase is not provided, it will be read from stdin.\n")
		fmt.Fprintf(os.Stderr, "\nExamples:\n")
		fmt.Fprintf(os.Stderr, "  %s /run/user/1000/ssh-multihop/passphrase.sock mysecret\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  echo 'mysecret' | %s /run/user/1000/ssh-multihop/passphrase.sock\n", os.Args[0])
		os.Exit(1)
	}

	socketPath := os.Args[1]
	var passphrase string

	// Get passphrase from command line or stdin
	if len(os.Args) == 3 {
		passphrase = os.Args[2]
	} else {
		// Read from stdin
		scanner := bufio.NewScanner(os.Stdin)
		if scanner.Scan() {
			passphrase = scanner.Text()
		} else {
			fmt.Fprintf(os.Stderr, "Error: failed to read passphrase from stdin\n")
			os.Exit(1)
		}
	}

	// Trim whitespace
	passphrase = strings.TrimSpace(passphrase)
	if passphrase == "" {
		fmt.Fprintf(os.Stderr, "Error: passphrase cannot be empty\n")
		os.Exit(1)
	}

	// Connect to passphrase socket
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error connecting to socket: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = conn.Close() }()

	// Send passphrase (fingerprint will be provided by server based on key)
	_, _ = fmt.Fprintf(conn, "%s\n", passphrase)

	// Read response
	response, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading response: %v\n", err)
		os.Exit(1)
	}

	response = strings.TrimSpace(response)
	if response == "OK" {
		fmt.Println("Passphrase sent successfully")
		os.Exit(0)
	} else {
		fmt.Fprintf(os.Stderr, "Error: %s\n", response)
		os.Exit(1)
	}
}
