package r1

import (
	"path/filepath"
	"testing"
)

func TestDeviceStore_FirstPair(t *testing.T) {
	path := filepath.Join(t.TempDir(), "r1-devices.json")
	store, err := OpenDeviceStore(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if store.Paired() {
		t.Error("fresh store should not be paired")
	}

	// First pair succeeds.
	dev, err := store.Pair(PairRequest{
		DeviceID:     "dev-1",
		PublicKey:    "pub-1",
		Role:         "node",
		Scopes:       []string{"node.basic"},
	})
	if err != nil {
		t.Fatalf("first pair: %v", err)
	}
	if dev.DeviceToken == "" {
		t.Error("expected a minted device token")
	}
	if len(dev.DeviceToken) < 32 {
		t.Errorf("device token too short (%d chars): %q", len(dev.DeviceToken), dev.DeviceToken)
	}
	if !store.Paired() {
		t.Error("store should be paired after first pair")
	}
}

func TestDeviceStore_SecondPairRejected(t *testing.T) {
	path := filepath.Join(t.TempDir(), "r1-devices.json")
	store, _ := OpenDeviceStore(path)
	_, err := store.Pair(PairRequest{DeviceID: "dev-1", PublicKey: "pub-1", Role: "node"})
	if err != nil {
		t.Fatalf("first pair: %v", err)
	}
	_, err = store.Pair(PairRequest{DeviceID: "dev-2", PublicKey: "pub-2", Role: "node"})
	if err == nil {
		t.Error("second pair should have been rejected")
	}
}

func TestDeviceStore_Persistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "r1-devices.json")
	store1, _ := OpenDeviceStore(path)
	dev1, err := store1.Pair(PairRequest{DeviceID: "dev-1", PublicKey: "pub-1", Role: "node"})
	if err != nil {
		t.Fatalf("pair: %v", err)
	}

	// Reopen.
	store2, err := OpenDeviceStore(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if !store2.Paired() {
		t.Error("reopened store should remember the pairing")
	}
	got, ok := store2.LookupByToken(dev1.DeviceToken)
	if !ok {
		t.Fatal("reopened store should resolve the minted token")
	}
	if got.DeviceID != "dev-1" || got.PublicKey != "pub-1" {
		t.Errorf("bad device: %+v", got)
	}
}

func TestDeviceStore_LookupMiss(t *testing.T) {
	path := filepath.Join(t.TempDir(), "r1-devices.json")
	store, _ := OpenDeviceStore(path)
	_, _ = store.Pair(PairRequest{DeviceID: "dev-1", PublicKey: "pub-1", Role: "node"})
	if _, ok := store.LookupByToken("nope"); ok {
		t.Error("lookup of unknown token should miss")
	}
}
