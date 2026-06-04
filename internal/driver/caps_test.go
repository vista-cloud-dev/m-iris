package driver

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	mdriver "github.com/vista-cloud-dev/m-driver-sdk"
)

// TestCaps_Golden pins the m-iris capability document (driver-contract.md §4).
// caps must be HONEST — it advertises only axes/verbs/transports that are
// actually wired, and grows as milestones land. This golden is the contract
// m-cli reads to decide what it may call.
func TestCaps_Golden(t *testing.T) {
	got, err := json.MarshalIndent(CapsDoc(), "", "  ")
	if err != nil {
		t.Fatalf("marshal caps: %v", err)
	}
	got = append(got, '\n')

	golden := filepath.Join("testdata", "caps.golden.json")
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		if err := os.WriteFile(golden, got, 0o644); err != nil {
			t.Fatalf("update golden: %v", err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	if string(got) != string(want) {
		t.Errorf("caps document drifted from golden\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

// TestCaps_Invariants asserts the fixed facts a consumer relies on regardless
// of which verbs are wired yet.
func TestCaps_Invariants(t *testing.T) {
	c := CapsDoc()
	if c.Engine != "iris" {
		t.Errorf("engine = %q, want iris", c.Engine)
	}
	if c.Contract != mdriver.ContractVersion {
		t.Errorf("contract = %q, want %q", c.Contract, mdriver.ContractVersion)
	}
	// IRIS is the only engine with a remote transport (Atelier REST).
	if !c.Features.Remote {
		t.Error("features.remote must be true for IRIS")
	}
	wantTransports := map[string]bool{"local": true, "docker": true, "remote": true}
	for _, tr := range c.Transports {
		delete(wantTransports, tr)
	}
	if len(wantTransports) != 0 {
		t.Errorf("missing transports: %v", wantTransports)
	}
}
