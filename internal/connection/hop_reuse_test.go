package connection

import (
	"testing"

	"github.com/hallelujah-shih/ssh-multihop/internal/tunnel"
)

func TestBuildPartialSignature(t *testing.T) {
	hops := []*tunnel.HopConfig{
		{Host: "t2", HostName: "t2.example.com", Port: 22, User: "user"},
		{Host: "vmr", HostName: "vmr.example.com", Port: 22, User: "user"},
	}

	sig1 := buildPartialSignature(hops[:1])
	if sig1.Username != "user" {
		t.Errorf("Expected Username 'user', got %s", sig1.Username)
	}
	if sig1.Hostname != "t2.example.com" {
		t.Errorf("Expected Hostname 't2.example.com', got %s", sig1.Hostname)
	}
	if sig1.Port != 22 {
		t.Errorf("Expected Port 22, got %d", sig1.Port)
	}
	if len(sig1.JumpChain) != 0 {
		t.Errorf("Expected empty JumpChain, got %v", sig1.JumpChain)
	}

	sig2 := buildPartialSignature(hops[:2])
	if sig2.Username != "user" {
		t.Errorf("Expected Username 'user', got %s", sig2.Username)
	}
	if sig2.Hostname != "vmr.example.com" {
		t.Errorf("Expected Hostname 'vmr.example.com', got %s", sig2.Hostname)
	}
	if sig2.Port != 22 {
		t.Errorf("Expected Port 22, got %d", sig2.Port)
	}
	if len(sig2.JumpChain) != 1 || sig2.JumpChain[0] != "t2" {
		t.Errorf("Expected JumpChain ['t2'], got %v", sig2.JumpChain)
	}
}

func TestBuildPartialSignature_Empty(t *testing.T) {
	sig := buildPartialSignature(nil)
	if sig.Username != "" || sig.Hostname != "" || sig.Port != 0 {
		t.Errorf("Expected empty signature for nil hops")
	}
}

func TestHopSourceConstants(t *testing.T) {
	if HopCreated != 0 {
		t.Errorf("HopCreated should be 0, got %d", HopCreated)
	}
	if HopReused != 1 {
		t.Errorf("HopReused should be 1, got %d", HopReused)
	}
}

func TestHopInfoStruct(t *testing.T) {
	info := HopInfo{
		Client:   nil,
		Source:   HopCreated,
		PoolHash: "test-hash",
	}

	if info.Source != HopCreated {
		t.Errorf("Expected HopCreated, got %d", info.Source)
	}
	if info.PoolHash != "test-hash" {
		t.Errorf("Expected 'test-hash', got %s", info.PoolHash)
	}
}

func TestHopInfo_HopReused(t *testing.T) {
	info := HopInfo{
		Client:   nil,
		Source:   HopReused,
		PoolHash: "reused-hash",
	}

	if info.Source != HopReused {
		t.Errorf("Expected HopReused, got %d", info.Source)
	}
	if info.PoolHash != "reused-hash" {
		t.Errorf("Expected 'reused-hash', got %s", info.PoolHash)
	}
}

func TestCleanupCreatedHops_Empty(t *testing.T) {
	hopInfos := []HopInfo{}
	cleanupCreatedHops(hopInfos)
}

func TestCleanupCreatedHops_OnlyCreated(t *testing.T) {
	hopInfos := []HopInfo{
		{Client: nil, Source: HopCreated, PoolHash: "hash1"},
		{Client: nil, Source: HopCreated, PoolHash: "hash2"},
	}
	cleanupCreatedHops(hopInfos)
}

func TestCleanupCreatedHops_OnlyReused(t *testing.T) {
	hopInfos := []HopInfo{
		{Client: nil, Source: HopReused, PoolHash: "hash1"},
		{Client: nil, Source: HopReused, PoolHash: "hash2"},
	}
	cleanupCreatedHops(hopInfos)
}

func TestCleanupCreatedHops_Mixed(t *testing.T) {
	hopInfos := []HopInfo{
		{Client: nil, Source: HopCreated, PoolHash: "hash1"},
		{Client: nil, Source: HopReused, PoolHash: "hash2"},
		{Client: nil, Source: HopCreated, PoolHash: "hash3"},
	}
	cleanupCreatedHops(hopInfos)
}

func TestPartialSignature_Hash(t *testing.T) {
	hops := []*tunnel.HopConfig{
		{Host: "t2", HostName: "t2.example.com", Port: 22, User: "user"},
	}

	sig := buildPartialSignature(hops)
	hash := sig.Hash()

	if hash == "" {
		t.Error("Hash should not be empty")
	}

	sig2 := buildPartialSignature(hops)
	hash2 := sig2.Hash()

	if hash != hash2 {
		t.Errorf("Same hops should produce same hash: %s != %s", hash, hash2)
	}
}

func TestPartialSignature_DifferentHops(t *testing.T) {
	hops1 := []*tunnel.HopConfig{
		{Host: "t2", HostName: "t2.example.com", Port: 22, User: "user"},
	}
	hops2 := []*tunnel.HopConfig{
		{Host: "dc4", HostName: "dc4.example.com", Port: 22, User: "user"},
	}

	sig1 := buildPartialSignature(hops1)
	sig2 := buildPartialSignature(hops2)

	if sig1.Hash() == sig2.Hash() {
		t.Error("Different hops should produce different hashes")
	}
}
