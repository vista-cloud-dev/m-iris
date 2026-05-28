package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAtelierSecretFilesWinAndAreTrimmed(t *testing.T) {
	dir := t.TempDir()
	tok := filepath.Join(dir, "token")
	pw := filepath.Join(dir, "pw")
	if err := os.WriteFile(tok, []byte("tok-123\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pw, []byte("  s3cret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c := &Conn{
		BaseURL: "https://h/", Namespace: "VISTA",
		Token: "inline-token", TokenFile: tok,
		Password: "inline-pw", PasswordFile: pw,
	}
	cfg, err := c.Atelier()
	if err != nil {
		t.Fatalf("Atelier: %v", err)
	}
	if cfg.Token != "tok-123" {
		t.Errorf("token = %q, want file contents trimmed (file wins)", cfg.Token)
	}
	if cfg.Password != "s3cret" {
		t.Errorf("password = %q, want file contents trimmed", cfg.Password)
	}
}

func TestAtelierInlineWhenNoFile(t *testing.T) {
	c := &Conn{Token: "inline", Password: "pw"}
	cfg, err := c.Atelier()
	if err != nil {
		t.Fatalf("Atelier: %v", err)
	}
	if cfg.Token != "inline" || cfg.Password != "pw" {
		t.Errorf("inline secrets not used: token=%q password=%q", cfg.Token, cfg.Password)
	}
}

func TestAtelierMissingSecretFileErrors(t *testing.T) {
	c := &Conn{BaseURL: "https://h/", Namespace: "VISTA", TokenFile: "/no/such/token/file"}
	if _, err := c.Atelier(); err == nil {
		t.Error("expected an error for a missing --token-file")
	}
}
