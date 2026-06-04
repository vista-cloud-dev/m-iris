// Package config holds the connection/behavior flags shared by every irissync
// command and resolves them into the atelier client and mirror layout.
//
// irissync is a standalone, portable binary: it reads config from flags + env
// only (Kong tags below), with secrets optionally sourced from files, so it can
// round-trip routines out of an IRIS system entirely on its own. An external
// orchestrator (e.g. m-cli) may resolve per-instance profiles and pass values
// down, but that is optional — never a dependency (liberation-binary-design §4).
package config

import (
	"fmt"
	"os"
	"strings"

	"github.com/vista-cloud-dev/m-iris/internal/atelier"
	"github.com/vista-cloud-dev/m-iris/internal/mirror"
)

// Conn is embedded in the root CLI struct, so its fields are global flags on
// every subcommand, and bound (via kong.Bind) so command Run methods receive a
// *Conn. Flags win over env; defaults fill the rest.
type Conn struct {
	BaseURL      string `name:"base-url" env:"M_IRIS_BASE_URL" help:"Atelier base URL, e.g. https://host:52773/api/atelier/v1/" placeholder:"URL"`
	Instance     string `env:"M_IRIS_INSTANCE" help:"Instance label used in the mirror path." placeholder:"NAME"`
	Namespace    string `env:"M_IRIS_NAMESPACE" help:"IRIS namespace to liberate." placeholder:"NS"`
	Mirror       string `env:"M_IRIS_MIRROR" default:".m-cache" help:"Mirror root directory."`
	Type         string `env:"M_IRIS_TYPE" enum:"mac,int,inc" default:"mac" help:"Routine type to liberate: mac (UDL/ObjectScript), int (classic MUMPS — e.g. ^%RI-loaded VistA), or inc (includes)."`
	Token        string `env:"M_IRIS_TOKEN" help:"OAuth2/bearer token for Atelier (sent as 'Authorization: Bearer …'; wins over --user/--password)." placeholder:"TOKEN"`
	TokenFile    string `name:"token-file" env:"M_IRIS_TOKEN_FILE" help:"Read the bearer token from this file (preferred over --token; keeps the secret out of argv/env)." placeholder:"PATH"`
	User         string `env:"M_IRIS_USER" help:"Atelier username (basic auth)."`
	Password     string `env:"M_IRIS_PASSWORD" help:"Atelier password (basic auth)."`
	PasswordFile string `name:"password-file" env:"M_IRIS_PASSWORD_FILE" help:"Read the basic-auth password from this file (preferred over --password)." placeholder:"PATH"`
	CAFile       string `name:"ca-file" env:"M_IRIS_CA_FILE" help:"Internal CA bundle (PEM) for in-boundary TLS." placeholder:"PATH"`
	ClientCert   string `name:"client-cert" env:"M_IRIS_CLIENT_CERT" help:"Client certificate (PEM) for mutual TLS." placeholder:"PATH"`
	ClientKey    string `name:"client-key" env:"M_IRIS_CLIENT_KEY" help:"Client private key (PEM) for mutual TLS." placeholder:"PATH"`
	Concurrency  int    `default:"8" help:"Parallel document GETs."`
	Filter       string `help:"Glob filter over docnames (e.g. 'DG*')." placeholder:"GLOB"`
	Package      string `help:"Restrict to a package / routine-name prefix." placeholder:"PREFIX"`
	DryRun       bool   `name:"dry-run" help:"Plan only; never write."`
	Porcelain    bool   `help:"Terse, machine-readable line output for list/status."`
}

// Need describes which inputs a command requires.
type Need struct {
	Network bool // talks to the server (needs base URL + namespace)
	Mirror  bool // touches the mirror (needs instance + namespace)
}

// Validate checks that the inputs a command needs are present and normalizes
// derived values. It returns a single message listing everything missing.
func (c *Conn) Validate(n Need) error {
	var missing []string
	if n.Network && c.BaseURL == "" {
		missing = append(missing, "--base-url")
	}
	if (n.Network || n.Mirror) && c.Namespace == "" {
		missing = append(missing, "--namespace")
	}
	if n.Mirror && c.Instance == "" {
		missing = append(missing, "--instance")
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required: %s", strings.Join(missing, ", "))
	}
	if c.Concurrency < 1 {
		c.Concurrency = 1
	}
	return nil
}

// Atelier builds the client config from the connection flags, reading the
// token/password from their *-file sources when set (a file wins over the
// inline flag/env, keeping the secret out of argv and the environment).
func (c *Conn) Atelier() (atelier.Config, error) {
	token, err := readSecret(c.Token, c.TokenFile)
	if err != nil {
		return atelier.Config{}, fmt.Errorf("--token-file: %w", err)
	}
	password, err := readSecret(c.Password, c.PasswordFile)
	if err != nil {
		return atelier.Config{}, fmt.Errorf("--password-file: %w", err)
	}
	return atelier.Config{
		BaseURL:    c.BaseURL,
		Namespace:  c.Namespace,
		Token:      token,
		User:       c.User,
		Password:   password,
		CAFile:     c.CAFile,
		ClientCert: c.ClientCert,
		ClientKey:  c.ClientKey,
	}, nil
}

// readSecret returns the trimmed contents of file when a path is given (the
// secret-by-file path, preferred), otherwise the inline value.
func readSecret(inline, file string) (string, error) {
	if file == "" {
		return inline, nil
	}
	b, err := os.ReadFile(file)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

// Layout builds the mirror layout from the connection flags.
func (c *Conn) Layout() mirror.Layout {
	return mirror.Layout{Root: c.Mirror, Instance: c.Instance, Namespace: c.Namespace}
}
