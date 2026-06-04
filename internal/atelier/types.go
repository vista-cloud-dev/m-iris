// Package atelier is the InterSystems Atelier REST API v1 client used by
// irissync to read and write IRIS routine source. It speaks the read-side
// endpoints needed by the source-axis gate (docnames + GET doc) and the
// write-back endpoints used by `irissync push` (PUT doc + action/compile).
// Implementation uses net/http + crypto/tls + crypto/x509 + encoding/json —
// no third-party HTTP/TLS/JSON.
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

// errCode is an Atelier error code, rendered as a JSON string by older servers
// and as a JSON number by IRIS 2026.1+. It is normalized to a string either way.
type errCode string

func (c *errCode) UnmarshalJSON(b []byte) error {
	if len(b) == 0 || string(b) == "null" {
		return nil
	}
	if b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		*c = errCode(s)
		return nil
	}
	*c = errCode(b) // numeric code → its literal text (e.g. 16006)
	return nil
}

// APIError decodes both forms Atelier servers have used across versions: the
// object form ({"error":"…","code":"…"}) and the bare-string form.
type APIError struct {
	Error string  `json:"error"`
	Code  errCode `json:"code,omitempty"`
	ID    string  `json:"id,omitempty"`
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
