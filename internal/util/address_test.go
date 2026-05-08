package util

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseAddress_ValidIPPort(t *testing.T) {
	ip, port, err := ParseAddress("127.0.0.1:8888")
	assert.NoError(t, err)
	assert.Equal(t, "127.0.0.1", ip)
	assert.Equal(t, 8888, port)
}

func TestParseAddress_AllInterfacesShorthand(t *testing.T) {
	ip, port, err := ParseAddress(":8888")
	assert.NoError(t, err)
	assert.Equal(t, "0.0.0.0", ip)
	assert.Equal(t, 8888, port)
}

func TestParseAddress_AllInterfacesExplicit(t *testing.T) {
	ip, port, err := ParseAddress("0.0.0.0:8888")
	assert.NoError(t, err)
	assert.Equal(t, "0.0.0.0", ip)
	assert.Equal(t, 8888, port)
}

func TestParseAddress_InvalidFormat(t *testing.T) {
	_, _, err := ParseAddress("invalid")
	assert.Error(t, err)
}

func TestParseAddress_InvalidIP(t *testing.T) {
	_, _, err := ParseAddress("999.999.999.999:80")
	assert.Error(t, err)
}

func TestParseAddress_InvalidPort(t *testing.T) {
	_, _, err := ParseAddress("127.0.0.1:abc")
	assert.Error(t, err)
}

func TestParseAddress_PortOutOfRange(t *testing.T) {
	_, _, err := ParseAddress("127.0.0.1:99999")
	assert.Error(t, err)
}
