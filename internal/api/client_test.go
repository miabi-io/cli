package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A page served in front of the panel (SSO gateway, WAF, wrong host) must be
// named as such, not reported as a JSON decode failure.
func TestClaimLoginTokenRejectsHTMLResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte("<!doctype html><html><head><title>Sign in</title></head><body>hi</body></html>"))
	}))
	defer srv.Close()

	c, err := New(Options{BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.ClaimLoginToken(context.Background(), "code")
	if err == nil {
		t.Fatal("expected an error for an HTML response")
	}
	for _, want := range []string{"not the Miabi API", "text/html", "Sign in"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

// An action with no payload must not read a gateway's 200 page as success.
func TestNoPayloadCallRejectsHTMLResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html><title>Checking your browser</title></html>"))
	}))
	defer srv.Close()

	c, err := New(Options{BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.StopApp(context.Background(), "ws", 1); err == nil {
		t.Fatal("expected an error for an HTML response")
	}
}

func TestRedirectsAreRefused(t *testing.T) {
	elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html><title>Login</title></html>"))
	}))
	defer elsewhere.Close()

	tests := []struct {
		name   string
		to     string
		status int
		want   string
	}{
		{"off-origin", elsewhere.URL + "/login", http.StatusFound, "intercepting"},
		{"method rewritten", "/api/v1/auth/login-token/claim", http.StatusMovedPermanently, "rewritten as GET"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, tt.to, tt.status)
			}))
			defer srv.Close()

			c, err := New(Options{BaseURL: srv.URL})
			if err != nil {
				t.Fatal(err)
			}
			_, err = c.ClaimLoginToken(context.Background(), "code")
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want it to mention %q", err, tt.want)
			}
		})
	}
}

// A failed action must fail the command, even though it decodes no payload.
func TestNoPayloadCallSurfacesAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"success":false,"error":{"code":"forbidden","message":"not allowed"}}`))
	}))
	defer srv.Close()

	c, err := New(Options{BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	err = c.StopApp(context.Background(), "ws", 1)
	if err == nil || !strings.Contains(err.Error(), "forbidden: not allowed") {
		t.Fatalf("error = %v, want the API's forbidden error", err)
	}
}

// A bodiless failure still has to be reported as one.
func TestEmptyBodyFailureIsAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	c, err := New(Options{BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.StopApp(context.Background(), "ws", 1); err == nil || !strings.Contains(err.Error(), "HTTP 502") {
		t.Fatalf("error = %v, want HTTP 502", err)
	}
}

// The envelope path stays intact: a normal API response still decodes.
func TestEnvelopeStillDecodes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"data":{"token":"tok-123"}}`))
	}))
	defer srv.Close()

	c, err := New(Options{BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	tok, err := c.ClaimLoginToken(context.Background(), "code")
	if err != nil {
		t.Fatal(err)
	}
	if tok.Token != "tok-123" {
		t.Errorf("token = %q, want tok-123", tok.Token)
	}
}
