package atelier

import (
	"errors"
	"fmt"
	"net/http"
)

// HTTPError is a non-2xx Atelier response surfaced as a typed error, so callers
// (doctor, lifecycle) can branch on the status — distinguishing "bad credential"
// (401) from "no privilege" (403) from "unreachable/other" (risks C3, C7) —
// instead of string-matching a message.
type HTTPError struct {
	Status int
	Method string
	Path   string
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("atelier: %s %s: HTTP %d", e.Method, e.Path, e.Status)
}

// IsUnauthorized reports whether err is (or wraps) a 401 — authentication failed
// (missing/invalid credential).
func IsUnauthorized(err error) bool { return hasStatus(err, http.StatusUnauthorized) }

// IsForbidden reports whether err is (or wraps) a 403 — authenticated but the
// credential lacks the required privilege.
func IsForbidden(err error) bool { return hasStatus(err, http.StatusForbidden) }

func hasStatus(err error, status int) bool {
	var he *HTTPError
	return errors.As(err, &he) && he.Status == status
}
