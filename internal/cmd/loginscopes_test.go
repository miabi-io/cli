package cmd

import (
	"strings"
	"testing"
)

func TestNormalizeLoginScopes(t *testing.T) {
	for _, tc := range []struct {
		in      []string
		want    string
		wantErr bool
	}{
		{nil, "", false},
		{[]string{"read"}, "read", false},
		{[]string{"READ", " write "}, "read,write", false},
		{[]string{"read", "read", "deploy"}, "read,deploy", false},
		{[]string{"read", ""}, "read", false},
		// A login token is never an administrative one, so the server refuses these — say so here,
		// before the browser round-trip, rather than after it.
		{[]string{"admin"}, "", true},
		{[]string{"*"}, "", true},
		{[]string{"reed"}, "", true},
	} {
		got, err := normalizeLoginScopes(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("normalizeLoginScopes(%v) = %v, want an error", tc.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("normalizeLoginScopes(%v): %v", tc.in, err)
			continue
		}
		if strings.Join(got, ",") != tc.want {
			t.Errorf("normalizeLoginScopes(%v) = %v, want %q", tc.in, got, tc.want)
		}
	}
}

// The point of the check: a server that ignores --scopes hands back its full default, and saving
// that as if it were narrowed is the false assurance the flag exists to remove.
func TestAssertScopes(t *testing.T) {
	for _, tc := range []struct {
		name    string
		want    []string
		got     []string
		wantErr bool
	}{
		{"nothing requested", nil, []string{"read", "write", "deploy"}, false},
		{"honoured exactly", []string{"read"}, []string{"read"}, false},
		{"narrower than asked is fine", []string{"read", "write"}, []string{"read"}, false},
		{"ignored by an old server", []string{"read"}, []string{"read", "write", "deploy"}, true},
		{"broader in one scope", []string{"read"}, []string{"read", "deploy"}, true},
	} {
		err := assertScopes(tc.want, tc.got)
		if (err != nil) != tc.wantErr {
			t.Errorf("%s: assertScopes(%v, %v) error = %v, wantErr %v", tc.name, tc.want, tc.got, err, tc.wantErr)
		}
	}
}

// Read-only mode is a client-side tool filter, not a server boundary. The advice has to say so
// when the token is broader, and stay quiet when the two agree.
func TestScopeAdvice(t *testing.T) {
	for _, tc := range []struct {
		name       string
		scopes     []string
		allowWrite bool
		wantMsg    bool
	}{
		{"read-only mode, read-only token", []string{"read"}, false, false},
		{"read-only mode, writable token", []string{"read", "write", "deploy"}, false, true},
		{"read-only mode, wildcard token", []string{"*"}, false, true},
		{"write mode, writable token", []string{"read", "write"}, true, false},
		{"write mode, deploy-only token", []string{"deploy"}, true, false},
		{"write mode, read-only token", []string{"read"}, true, true},
		{"empty scope set is read-only", nil, false, false},
		{"empty scope set cannot write", nil, true, true},
	} {
		got := scopeAdvice(tc.scopes, tc.allowWrite)
		if (got != "") != tc.wantMsg {
			t.Errorf("%s: scopeAdvice(%v, %v) = %q, wantMsg %v", tc.name, tc.scopes, tc.allowWrite, got, tc.wantMsg)
		}
	}
}
