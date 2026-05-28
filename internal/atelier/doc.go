package atelier

import (
	"context"
	"encoding/json"
	"fmt"
)

// GetDoc fetches one document's source by docname (e.g. "DGREG.mac"). The
// returned Doc.Content is the server's line array — plain UDL/Atelier text. A
// binary (enc=true) payload is rejected: routines are text, and the mirror must
// stay tree-sitter-parseable.
func (c *Client) GetDoc(ctx context.Context, name string) (*Doc, error) {
	u := c.endpoint(c.namespace, "doc", name)

	var env Envelope
	if err := c.get(ctx, u, &env); err != nil {
		return nil, err
	}
	if len(env.Result) == 0 {
		return nil, fmt.Errorf("atelier: empty result for doc %q", name)
	}
	var doc Doc
	if err := json.Unmarshal(env.Result, &doc); err != nil {
		return nil, fmt.Errorf("atelier: decode doc %q: %w", name, err)
	}
	if doc.Enc {
		return nil, fmt.Errorf("atelier: doc %q is binary-encoded (enc=true); not a text routine", name)
	}
	return &doc, nil
}
