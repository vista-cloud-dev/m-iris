// Package config holds the connection/behavior flags shared by every irissync
// command and resolves them into the atelier client and mirror layout. The
// binary reads config from flags + env only (Kong env tags below); m-cli owns
// .m-cli.toml and passes resolved values down (liberation-binary-design §4).
package config

import (
	"fmt"
	"strings"

	"github.com/vista-cloud-dev/irissync/internal/atelier"
	"github.com/vista-cloud-dev/irissync/internal/mirror"
)

// Conn is embedded in the root CLI struct, so its fields are global flags on
// every subcommand, and bound (via kong.Bind) so command Run methods receive a
// *Conn. Flags win over env; defaults fill the rest.
type Conn struct {
	BaseURL     string `name:"base-url" env:"IRISSYNC_BASE_URL" help:"Atelier base URL, e.g. https://host:52773/api/atelier/v1/" placeholder:"URL"`
	Instance    string `env:"IRISSYNC_INSTANCE" help:"Instance label used in the mirror path." placeholder:"NAME"`
	Namespace   string `env:"IRISSYNC_NAMESPACE" help:"IRIS namespace to liberate." placeholder:"NS"`
	Mirror      string `env:"IRISSYNC_MIRROR" default:".m-cache" help:"Mirror root directory."`
	Type        string `env:"IRISSYNC_TYPE" enum:"mac,int,inc" default:"mac" help:"Routine type to liberate: mac (UDL/ObjectScript), int (classic MUMPS — e.g. ^%RI-loaded VistA), or inc (includes)."`
	User        string `env:"IRISSYNC_USER" help:"Atelier username (basic auth)."`
	Password    string `env:"IRISSYNC_PASSWORD" help:"Atelier password (basic auth)."`
	CAFile      string `name:"ca-file" env:"IRISSYNC_CA_FILE" help:"Internal CA bundle (PEM) for in-boundary TLS." placeholder:"PATH"`
	ClientCert  string `name:"client-cert" env:"IRISSYNC_CLIENT_CERT" help:"Client certificate (PEM) for mutual TLS." placeholder:"PATH"`
	ClientKey   string `name:"client-key" env:"IRISSYNC_CLIENT_KEY" help:"Client private key (PEM) for mutual TLS." placeholder:"PATH"`
	Concurrency int    `default:"8" help:"Parallel document GETs."`
	Filter      string `help:"Glob filter over docnames (e.g. 'DG*')." placeholder:"GLOB"`
	Package     string `help:"Restrict to a package / routine-name prefix." placeholder:"PREFIX"`
	DryRun      bool   `name:"dry-run" help:"Plan only; never write."`
	Porcelain   bool   `help:"Terse, machine-readable line output for list/status."`
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

// Atelier builds the client config from the connection flags.
func (c *Conn) Atelier() atelier.Config {
	return atelier.Config{
		BaseURL:    c.BaseURL,
		Namespace:  c.Namespace,
		User:       c.User,
		Password:   c.Password,
		CAFile:     c.CAFile,
		ClientCert: c.ClientCert,
		ClientKey:  c.ClientKey,
	}
}

// Layout builds the mirror layout from the connection flags.
func (c *Conn) Layout() mirror.Layout {
	return mirror.Layout{Root: c.Mirror, Instance: c.Instance, Namespace: c.Namespace}
}
