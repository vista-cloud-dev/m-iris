package atelier

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// ServerInfo is the Atelier root probe result (GET /api/atelier/): the engine
// version, the Atelier API level, and the namespaces the credential can see. It
// is the substrate for lifecycle status / health probes / doctor / meta info on
// the remote transport.
type ServerInfo struct {
	Version    string   `json:"version"`
	API        int      `json:"api"`
	Namespaces []string `json:"namespaces,omitempty"`
}

// ServerInfo issues GET against the UNVERSIONED Atelier root and decodes the
// server descriptor. The version-prefixed root (…/api/atelier/v1/) 404s on
// modern IRIS (e.g. 2026.1); the descriptor lives at …/api/atelier/, the
// version-discovery endpoint present in every Atelier release. A 401/403 comes
// back as a typed *HTTPError (see IsUnauthorized / IsForbidden) so doctor can
// report auth state precisely.
func (c *Client) ServerInfo(ctx context.Context) (*ServerInfo, error) {
	u := *c.base // base ends in /api/atelier/v1/ — strip the version segment
	p := strings.TrimRight(u.Path, "/")
	if i := strings.LastIndex(p, "/"); i >= 0 {
		p = p[:i+1]
	}
	u.Path = p

	var env Envelope
	if err := c.get(ctx, &u, &env); err != nil {
		return nil, err
	}
	if len(env.Result) == 0 {
		return nil, fmt.Errorf("atelier: empty root response")
	}
	var wrapped struct {
		Content ServerInfo `json:"content"`
	}
	if err := json.Unmarshal(env.Result, &wrapped); err != nil {
		return nil, fmt.Errorf("atelier: decode root response: %w", err)
	}
	return &wrapped.Content, nil
}
