// Package atelier is the InterSystems Atelier REST API v1 client used by
// irissync to read IRIS routine source. It speaks only the read-side endpoints
// needed by the source-axis gate (docnames + doc); write-back (PUT + compile)
// lands with `irissync push` (stage 2.1). Implementation uses net/http +
// crypto/tls + crypto/x509 + encoding/json — no third-party HTTP/TLS/JSON.
package atelier

import "encoding/json"

// Envelope is the standard Atelier v1 response wrapper: every endpoint returns
// {status, console, result}. Result is decoded per-endpoint.
type Envelope struct {
	Status  Status            `json:"status"`
	Console []json.RawMessage `json:"console,omitempty"`
	Result  json.RawMessage   `json:"result"`
}

// Status carries server-side errors and a summary. Errors are surfaced as a Go
// error by the client so callers branch on err, not on payload shape.
type Status struct {
	Errors  []APIError `json:"errors"`
	Summary string     `json:"summary,omitempty"`
}

// APIError decodes both forms Atelier servers have used across versions: the
// object form ({"error":"…","code":"…"}) and the bare-string form.
type APIError struct {
	Error string `json:"error"`
	Code  string `json:"code,omitempty"`
	ID    string `json:"id,omitempty"`
}

// UnmarshalJSON accepts either a JSON string or a JSON object.
func (e *APIError) UnmarshalJSON(b []byte) error {
	if len(b) > 0 && b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		e.Error = s
		return nil
	}
	type alias APIError // avoid recursing into this method
	var a alias
	if err := json.Unmarshal(b, &a); err != nil {
		return err
	}
	*e = APIError(a)
	return nil
}

// DocName is one entry from a docnames listing.
type DocName struct {
	Name string `json:"name"`
	Cat  string `json:"cat,omitempty"`
	TS   string `json:"ts,omitempty"`
	DB   string `json:"db,omitempty"`
	Upd  bool   `json:"upd,omitempty"`
	Gen  bool   `json:"gen,omitempty"`
}

// Doc is a single document fetched from the server. Content is the source as a
// line array — plain UDL/Atelier text for routines, never the XML export.
type Doc struct {
	Name    string   `json:"name"`
	TS      string   `json:"ts,omitempty"`
	Cat     string   `json:"cat,omitempty"`
	DB      string   `json:"db,omitempty"`
	Enc     bool     `json:"enc"`
	Content []string `json:"content"`
}
