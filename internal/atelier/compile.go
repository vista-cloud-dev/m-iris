package atelier

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
)

// CompileResult reports the outcome of a compile action. Diagnostics holds any
// compile errors/warnings the server returned (from status.errors and per-doc
// status). The transport call succeeds even when the compile fails — a compile
// error is data, not an HTTP error — so push can record the new server state
// and still flag the failure (liberation-binary-design §5).
type CompileResult struct {
	Diagnostics []string
	Console     []string
}

// OK reports whether the compile produced no diagnostics.
func (r CompileResult) OK() bool { return len(r.Diagnostics) == 0 }

// compileDoc is one per-document entry in the compile result.
type compileDoc struct {
	Name   string `json:"name"`
	Status string `json:"status,omitempty"`
	TS     string `json:"ts,omitempty"`
}

// Compile compiles the named documents (POST …/action/compile) and reports any
// diagnostics. flags is the IRIS compile-flags string (e.g. "cuk": c=compile,
// u=update-only/skip-related, k=keep-generated-source). The request body is a
// JSON array of docnames, the Atelier v1 convention.
func (c *Client) Compile(ctx context.Context, names []string, flags string) (*CompileResult, error) {
	u := c.endpoint(c.namespace, "action", "compile")
	if flags == "" {
		flags = "cuk"
	}
	q := url.Values{}
	q.Set("flags", flags)
	u.RawQuery = q.Encode()

	body, err := json.Marshal(names)
	if err != nil {
		return nil, fmt.Errorf("atelier: encode compile request: %w", err)
	}

	// Compile errors arrive inside the envelope's status.errors; collect them as
	// diagnostics rather than failing the call, so do() must not turn them into
	// a Go error. Decode the raw response here.
	var env compileEnvelope
	if err := c.do(ctx, "POST", u, body, &env.Envelope); err != nil {
		// do() surfaces status.errors as an error. For compile, that *is* the
		// diagnostic set — recover it instead of failing.
		if diags := env.Status.diagnostics(); len(diags) > 0 {
			res := &CompileResult{Diagnostics: diags}
			res.Console = consoleStrings(env.Console)
			collectDocStatuses(env.Result, res)
			return res, nil
		}
		return nil, err
	}

	res := &CompileResult{Console: consoleStrings(env.Console)}
	res.Diagnostics = env.Status.diagnostics()
	collectDocStatuses(env.Result, res)
	return res, nil
}

// compileEnvelope wraps the shared Envelope so do() can decode into it.
type compileEnvelope struct {
	Envelope
}

// diagnostics flattens status.errors into human-readable strings.
func (s Status) diagnostics() []string {
	var out []string
	for _, e := range s.Errors {
		if e.Error == "" {
			continue
		}
		if e.Code != "" {
			out = append(out, fmt.Sprintf("%s (%s)", e.Error, e.Code))
		} else {
			out = append(out, e.Error)
		}
	}
	return out
}

// collectDocStatuses appends any non-empty per-document compile status to the
// result's diagnostics.
func collectDocStatuses(result json.RawMessage, res *CompileResult) {
	if len(result) == 0 {
		return
	}
	var wrapped struct {
		Content []compileDoc `json:"content"`
	}
	var docs []compileDoc
	if err := json.Unmarshal(result, &wrapped); err == nil && wrapped.Content != nil {
		docs = wrapped.Content
	} else {
		if err := json.Unmarshal(result, &docs); err != nil {
			return
		}
	}
	for _, d := range docs {
		if d.Status != "" {
			res.Diagnostics = append(res.Diagnostics, fmt.Sprintf("%s: %s", d.Name, d.Status))
		}
	}
}

func consoleStrings(raw []json.RawMessage) []string {
	out := make([]string, 0, len(raw))
	for _, r := range raw {
		var s string
		if err := json.Unmarshal(r, &s); err == nil {
			out = append(out, s)
		}
	}
	return out
}
