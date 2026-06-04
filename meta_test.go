package main

import (
	"encoding/json"
	"testing"

	"github.com/vista-cloud-dev/m-iris/internal/config"
	"github.com/vista-cloud-dev/m-iris/internal/driver"
)

// TestCapsCommand_EmitsHonestDocument runs `meta caps` and asserts the envelope
// carries the live capability document — the thing m-cli reads before calling
// any optional verb.
func TestCapsCommand_EmitsHonestDocument(t *testing.T) {
	cc, buf := jsonCtx()
	if err := (capsCmd{}).Run(cc); err != nil {
		t.Fatalf("caps: %v", err)
	}
	var env struct {
		OK   bool        `json:"ok"`
		Data driver.Caps `json:"data"`
	}
	if err := json.Unmarshal(buf.Bytes(), &env); err != nil {
		t.Fatalf("decode caps envelope: %v\n%s", err, buf.String())
	}
	if !env.OK {
		t.Error("caps envelope ok=false")
	}
	if env.Data.Engine != "iris" || env.Data.Contract != driver.ContractVersion {
		t.Errorf("caps data = %+v, want engine=iris contract=%s", env.Data, driver.ContractVersion)
	}
	if !env.Data.Features["remote"] {
		t.Error("caps must advertise the remote transport for IRIS")
	}
	// Honesty: every axis caps advertises must list at least one verb.
	for axis, verbs := range env.Data.Axes {
		if len(verbs) == 0 {
			t.Errorf("axis %q advertised with no verbs", axis)
		}
	}
}

// TestInfoCommand_ReportsIdentity runs `meta info` and asserts the driver
// identity + resolved target are reported without contacting an engine.
func TestInfoCommand_ReportsIdentity(t *testing.T) {
	cc, buf := jsonCtx()
	conn := &config.Conn{Namespace: "VISTA", BaseURL: "https://iris.example:52773/api/atelier/v1/"}
	if err := (infoCmd{}).Run(cc, conn); err != nil {
		t.Fatalf("info: %v", err)
	}
	var env struct {
		Data infoResult `json:"data"`
	}
	if err := json.Unmarshal(buf.Bytes(), &env); err != nil {
		t.Fatalf("decode info envelope: %v\n%s", err, buf.String())
	}
	if env.Data.Engine != "iris" || env.Data.Contract != driver.ContractVersion {
		t.Errorf("info = %+v, want engine=iris contract=%s", env.Data, driver.ContractVersion)
	}
	if env.Data.Namespace != "VISTA" {
		t.Errorf("info namespace = %q, want VISTA", env.Data.Namespace)
	}
}
