package atelier

import (
	"context"
	"encoding/json"
	"fmt"
)

// Query runs a SQL statement via POST {namespace}/action/query and returns the
// result rows (result.content), each a column→value map.
//
// This endpoint is the ENTIRE remote ObjectScript substrate for m-iris: Atelier
// exposes no raw "run ObjectScript" endpoint, and Go has no official IRIS Native
// SDK, so all remote exec / data / cover / admin go through SQL — by calling a
// role-gated, parameterized runner class (see internal/remote) whose methods are
// projected as SQL procedures, then reading results back out of a result global
// (risk B2). Parameters are bound server-side (never string-concatenated) so the
// runner is not a SQL-injection surface.
func (c *Client) Query(ctx context.Context, sql string, params ...string) ([]map[string]string, error) {
	u := c.endpoint(c.namespace, "action", "query")

	// Atelier wants parameters as a JSON array; nil-safe so an empty list still
	// marshals as [] rather than null.
	ps := params
	if ps == nil {
		ps = []string{}
	}
	body, err := json.Marshal(struct {
		Query      string   `json:"query"`
		Parameters []string `json:"parameters"`
	}{Query: sql, Parameters: ps})
	if err != nil {
		return nil, fmt.Errorf("atelier: encode query: %w", err)
	}

	var env Envelope
	if err := c.do(ctx, "POST", u, body, &env); err != nil {
		return nil, err
	}
	return decodeQueryContent(env.Result)
}

// decodeQueryContent pulls the row set out of an action/query result. The result
// is {content:[ {col:val,…}, … ]}; values come back as JSON scalars, which we
// normalize to strings (SQL over Atelier is string-in/string-out for the runner).
func decodeQueryContent(result json.RawMessage) ([]map[string]string, error) {
	if len(result) == 0 {
		return nil, nil
	}
	var wrapped struct {
		Content []map[string]json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(result, &wrapped); err != nil {
		return nil, fmt.Errorf("atelier: decode query result: %w", err)
	}
	rows := make([]map[string]string, 0, len(wrapped.Content))
	for _, raw := range wrapped.Content {
		row := make(map[string]string, len(raw))
		for col, v := range raw {
			row[col] = scalarString(v)
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// scalarString renders a JSON scalar as the string a SQL column carried. A JSON
// string is unquoted; anything else (number, bool, null) keeps its literal text.
func scalarString(v json.RawMessage) string {
	if len(v) == 0 {
		return ""
	}
	if v[0] == '"' {
		var s string
		if err := json.Unmarshal(v, &s); err == nil {
			return s
		}
	}
	if string(v) == "null" {
		return ""
	}
	return string(v)
}
