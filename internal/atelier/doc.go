package atelier

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
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

// PutResult is what the server reports after storing a document: at minimum the
// new server timestamp, used to update the manifest after a push.
type PutResult struct {
	Name string
	TS   string
}

// putBody is the Atelier PUT payload: a plain UDL/Atelier line array, never the
// XML export wrapper (which the mirror writer already refuses on the read side).
type putBody struct {
	Enc     bool     `json:"enc"`
	Content []string `json:"content"`
}

// PutDoc stores a document's source on the server (PUT …/doc/{name}). content
// is the routine's line array (plain UDL/Atelier text). It returns the new
// server timestamp so the caller can refresh its manifest entry. PutDoc only
// stores the source; compilation is a separate Compile call (the write is not
// validated until then) — see liberation-binary-design §5.
func (c *Client) PutDoc(ctx context.Context, name string, content []string) (*PutResult, error) {
	u := c.endpoint(c.namespace, "doc", name)
	payload, err := json.Marshal(putBody{Enc: false, Content: content})
	if err != nil {
		return nil, fmt.Errorf("atelier: encode doc %q: %w", name, err)
	}
	var env Envelope
	if err := c.do(ctx, "PUT", u, payload, &env); err != nil {
		return nil, err
	}
	res := &PutResult{Name: name}
	if len(env.Result) > 0 {
		var doc Doc
		if err := json.Unmarshal(env.Result, &doc); err == nil {
			res.TS = doc.TS
			if doc.Name != "" {
				res.Name = doc.Name
			}
		}
	}
	return res, nil
}

// Stat fetches a document's current metadata (timestamp) without committing to
// keep its body. It is used by push's conflict-check to read the live server
// state just before a write. A missing document returns ok=false and no error.
func (c *Client) Stat(ctx context.Context, name string) (DocName, bool, error) {
	u := c.endpoint(c.namespace, "doc", name)
	var env Envelope
	if err := c.get(ctx, u, &env); err != nil {
		// A "does not exist" error is the not-found signal, not a failure.
		if isNotFound(err) {
			return DocName{}, false, nil
		}
		return DocName{}, false, err
	}
	if len(env.Result) == 0 {
		return DocName{}, false, nil
	}
	var doc Doc
	if err := json.Unmarshal(env.Result, &doc); err != nil {
		return DocName{}, false, fmt.Errorf("atelier: decode doc %q: %w", name, err)
	}
	return DocName{Name: doc.Name, TS: doc.TS, Cat: doc.Cat, DB: doc.DB}, true, nil
}

// isNotFound reports whether an error is Atelier's "document does not exist"
// signal (the server returns it in status.errors, mapped to a Go error).
func isNotFound(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "does not exist") ||
		strings.Contains(msg, "#5002") ||
		strings.Contains(msg, "not found")
}
