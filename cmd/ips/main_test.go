package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHealthcheck(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusServiceUnavailable} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/readyz" {
					t.Errorf("healthcheck path = %s", r.URL.Path)
				}
				w.WriteHeader(status)
			}))
			defer server.Close()
			err := healthcheck(strings.TrimPrefix(server.URL, "http://"))
			if (err == nil) != (status == http.StatusOK) {
				t.Fatalf("status %d: error = %v", status, err)
			}
		})
	}
}
