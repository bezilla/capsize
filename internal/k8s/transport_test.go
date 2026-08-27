package k8s

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

type recordingRT struct{ called bool }

func (r *recordingRT) RoundTrip(*http.Request) (*http.Response, error) {
	r.called = true
	return &http.Response{StatusCode: 200, Body: http.NoBody}, nil
}

func TestReadOnlyTransportAllowsReads(t *testing.T) {
	for _, m := range []string{http.MethodGet, http.MethodHead, http.MethodOptions} {
		base := &recordingRT{}
		rt := &readOnlyTransport{base: base}
		req := httptest.NewRequest(m, "https://example.invalid/api/v1/pods", nil)
		if _, err := rt.RoundTrip(req); err != nil {
			t.Fatalf("%s should be allowed, got %v", m, err)
		}
		if !base.called {
			t.Fatalf("%s never reached the base transport", m)
		}
	}
}

func TestReadOnlyTransportRefusesWrites(t *testing.T) {
	for _, m := range []string{
		http.MethodPost, http.MethodPut, http.MethodPatch,
		http.MethodDelete, http.MethodConnect, http.MethodTrace,
	} {
		base := &recordingRT{}
		rt := &readOnlyTransport{base: base}
		req := httptest.NewRequest(m, "https://example.invalid/api/v1/pods", nil)
		_, err := rt.RoundTrip(req)
		if err == nil {
			t.Fatalf("%s was allowed through; the read-only guarantee is broken", m)
		}
		var wa *ErrWriteAttempt
		if !errors.As(err, &wa) {
			t.Fatalf("%s: want *ErrWriteAttempt, got %T", m, err)
		}
		if base.called {
			t.Fatalf("%s reached the base transport before being refused", m)
		}
	}
}
