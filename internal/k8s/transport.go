package k8s

import (
	"fmt"
	"net/http"
)

// readOnlyMethods is the complete set of HTTP verbs capsize is permitted to
// send. Kubernetes WATCH rides on GET, so streaming still works.
var readOnlyMethods = map[string]bool{
	http.MethodGet:     true,
	http.MethodHead:    true,
	http.MethodOptions: true,
}

// ErrWriteAttempt is returned by the transport when something in the process
// tries to send a mutating request. It should be unreachable; it exists so
// that if it ever does happen, capsize fails loudly at the socket instead of
// quietly changing someone's cluster.
type ErrWriteAttempt struct {
	Method string
	Path   string
}

func (e *ErrWriteAttempt) Error() string {
	return fmt.Sprintf("capsize is read-only: refused to send %s %s", e.Method, e.Path)
}

// readOnlyTransport is the second of capsize's two read-only guarantees.
//
// The first is structural: internal/k8s exposes no method that writes, so no
// caller outside this package can reach a write verb at all, and a test
// (internal/guard) fails the build if a write-shaped call appears anywhere in
// the tree. This transport is the backstop for anything that slips past the
// compiler - a vendored library, a reflection-based helper, a future
// dependency that phones home.
type readOnlyTransport struct {
	base http.RoundTripper
}

func (t *readOnlyTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if !readOnlyMethods[req.Method] {
		return nil, &ErrWriteAttempt{Method: req.Method, Path: req.URL.Path}
	}
	return t.base.RoundTrip(req)
}
