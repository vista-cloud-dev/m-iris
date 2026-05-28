package atelier

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
)

// DocNames lists routine documents (cat=RTN, type=mac) in the namespace.
// Generated documents are excluded; the optional server-side filter is a SQL
// match expression. irissync also filters client-side (glob + package prefix),
// so callers may pass "" here and filter the returned slice instead.
func (c *Client) DocNames(ctx context.Context, filter string) ([]DocName, error) {
	u := c.endpoint(c.namespace, "docnames", "RTN", "mac")
	q := url.Values{}
	q.Set("generated", "0")
	if filter != "" {
		q.Set("filter", filter)
	}
	u.RawQuery = q.Encode()

	var env Envelope
	if err := c.get(ctx, u, &env); err != nil {
		return nil, err
	}
	if len(env.Result) == 0 {
		return nil, nil
	}

	// The result is normally {content: [...]}, but some servers return a bare
	// array; accept both.
	var wrapped struct {
		Content []DocName `json:"content"`
	}
	if err := json.Unmarshal(env.Result, &wrapped); err == nil && wrapped.Content != nil {
		return wrapped.Content, nil
	}
	var bare []DocName
	if err := json.Unmarshal(env.Result, &bare); err != nil {
		return nil, fmt.Errorf("atelier: decode docnames result: %w", err)
	}
	return bare, nil
}
