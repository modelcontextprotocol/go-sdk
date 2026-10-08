// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package oauthex

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestSplitChallenges(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{
			name:  "single challenge no params",
			input: `Basic`,
			want:  []string{`Basic`},
		},
		{
			name:  "single challenge with params",
			input: `Bearer realm="example.com", error="invalid_token"`,
			want:  []string{`Bearer realm="example.com", error="invalid_token"`},
		},
		{
			name:  "single challenge with comma in quoted string",
			input: `Bearer realm="example, with comma"`,
			want:  []string{`Bearer realm="example, with comma"`},
		},
		{
			name:  "two challenges",
			input: `Basic, Bearer realm="example"`,
			want:  []string{`Basic`, ` Bearer realm="example"`},
		},
		{
			name:  "multiple challenges complex",
			input: `Newauth realm="apps", Basic, Bearer realm="example.com", error="invalid_token"`,
			want:  []string{`Newauth realm="apps"`, ` Basic`, ` Bearer realm="example.com", error="invalid_token"`},
		},
		{
			name:  "challenge with escaped quote",
			input: `Bearer realm="example \"quoted\""`,
			want:  []string{`Bearer realm="example \"quoted\""`},
		},
		{
			name:  "quoted value ending in escaped backslash",
			input: `Basic realm="C:\\", Bearer error="insufficient_scope"`,
			want:  []string{`Basic realm="C:\\"`, ` Bearer error="insufficient_scope"`},
		},
		{
			name:  "empty list element between params",
			input: `Bearer realm="example",, error="insufficient_scope"`,
			want:  []string{`Bearer realm="example",, error="insufficient_scope"`},
		},
		{
			name:  "empty list element between challenges",
			input: `Basic, , Bearer realm="example"`,
			want:  []string{`Basic`, ` `, ` Bearer realm="example"`},
		},
		{
			name:  "empty input",
			input: "",
			want:  []string{""},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := splitChallenges(tt.input)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("splitChallenges() = %v, want %v", got, tt.want)
			}
		})
	}
}

// A quoted string may end in an escaped backslash. The quote after it closes
// the string, so the comma that follows still separates two challenges and
// the Bearer challenge the auth handlers look for is not lost.
func TestParseWWWAuthenticateEscapedBackslash(t *testing.T) {
	got, err := ParseWWWAuthenticate([]string{
		`Basic realm="C:\\", Bearer error="insufficient_scope", scope="files:write"`,
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []Challenge{
		{Scheme: "basic", Params: map[string]string{"realm": `C:\`}},
		{Scheme: "bearer", Params: map[string]string{
			"error": "insufficient_scope",
			"scope": "files:write",
		}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ParseWWWAuthenticate() = %+v, want %+v", got, want)
	}
}

// A quoted string may be empty. An empty realm or error_description must not
// make the whole header unparseable, or the auth handlers never see the
// resource_metadata parameter that follows it.
func TestParseWWWAuthenticateEmptyQuotedValue(t *testing.T) {
	got, err := ParseWWWAuthenticate([]string{
		`Bearer realm="", error_description="", resource_metadata="https://example.com/.well-known/oauth-protected-resource"`,
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []Challenge{
		{Scheme: "bearer", Params: map[string]string{
			"realm":             "",
			"error_description": "",
			"resource_metadata": "https://example.com/.well-known/oauth-protected-resource",
		}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ParseWWWAuthenticate() = %+v, want %+v", got, want)
	}
}

// Recipients must accept empty list elements (RFC 9110, section 5.6.1).
// An extra comma must not move error="insufficient_scope" out of the Bearer
// challenge, or the auth handler would not start step-up authorization.
func TestParseWWWAuthenticateEmptyListElements(t *testing.T) {
	got, err := ParseWWWAuthenticate([]string{
		`, Basic realm="a",, Bearer , realm="b",, error="insufficient_scope",`,
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []Challenge{
		{Scheme: "basic", Params: map[string]string{"realm": "a"}},
		{Scheme: "bearer", Params: map[string]string{
			"realm": "b",
			"error": "insufficient_scope",
		}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ParseWWWAuthenticate() = %+v, want %+v", got, want)
	}
}

func TestSplitChallengesError(t *testing.T) {
	if _, err := splitChallenges(`"Bearer"`); err == nil {
		t.Fatal("got nil, want error")
	}
}

func TestParseSingleChallenge(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    Challenge
		wantErr bool
	}{
		{
			name:  "scheme only",
			input: "Basic",
			want: Challenge{
				Scheme: "basic",
			},
			wantErr: false,
		},
		{
			name:  "scheme with one quoted param",
			input: `Bearer realm="example.com"`,
			want: Challenge{
				Scheme: "bearer",
				Params: map[string]string{"realm": "example.com"},
			},
			wantErr: false,
		},
		{
			name:  "scheme with one unquoted param",
			input: `Bearer realm=example.com`,
			want: Challenge{
				Scheme: "bearer",
				Params: map[string]string{"realm": "example.com"},
			},
			wantErr: false,
		},
		{
			name:  "scheme with multiple params",
			input: `Bearer realm="example", error="invalid_token", error_description="The token expired"`,
			want: Challenge{
				Scheme: "bearer",
				Params: map[string]string{
					"realm":             "example",
					"error":             "invalid_token",
					"error_description": "The token expired",
				},
			},
			wantErr: false,
		},
		{
			name:  "scheme with multiple unquoted params",
			input: `Bearer realm=example, error=invalid_token, error_description=The token expired`,
			want: Challenge{
				Scheme: "bearer",
				Params: map[string]string{
					"realm":             "example",
					"error":             "invalid_token",
					"error_description": "The token expired",
				},
			},
			wantErr: false,
		},
		{
			name:  "case-insensitive scheme and keys",
			input: `BEARER ReAlM="example"`,
			want: Challenge{
				Scheme: "bearer",
				Params: map[string]string{"realm": "example"},
			},
			wantErr: false,
		},
		{
			name:  "param with escaped quote",
			input: `Bearer realm="example \"foo\" bar"`,
			want: Challenge{
				Scheme: "bearer",
				Params: map[string]string{"realm": `example "foo" bar`},
			},
			wantErr: false,
		},
		{
			name:  "param without quotes (token)",
			input: "Bearer realm=example.com",
			want: Challenge{
				Scheme: "bearer",
				Params: map[string]string{"realm": "example.com"},
			},
			wantErr: false,
		},
		{
			name:    "malformed param - no value",
			input:   "Bearer realm=",
			wantErr: true,
		},
		{
			name:  "empty quoted param",
			input: `Bearer realm="", error="invalid_token"`,
			want: Challenge{
				Scheme: "bearer",
				Params: map[string]string{"realm": "", "error": "invalid_token"},
			},
			wantErr: false,
		},
		{
			name:    "malformed param - no value before comma",
			input:   `Bearer realm=, error="invalid_token"`,
			wantErr: true,
		},
		{
			name:    "malformed param - unterminated quote",
			input:   `Bearer realm="example`,
			wantErr: true,
		},
		{
			name:    "malformed param - missing comma",
			input:   `Bearer realm="a" error="b"`,
			wantErr: true,
		},
		{
			name:    "malformed param - initial equal",
			input:   `Bearer ="a"`,
			wantErr: true,
		},
		{
			name:    "empty input",
			input:   "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseSingleChallenge(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseSingleChallenge() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("parseSingleChallenge() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGetProtectedResourceMetadata(t *testing.T) {
	ctx := t.Context()
	t.Run("Success", func(t *testing.T) {
		h := &fakeResourceHandler{}
		server := httptest.NewTLSServer(h)
		h.installHandlers(server.URL)
		client := server.Client()
		metadataURL := server.URL + "/.well-known/oauth-protected-resource"
		prm, err := GetProtectedResourceMetadata(ctx, metadataURL, server.URL, client)
		if err != nil {
			t.Fatal(err)
		}
		if prm == nil {
			t.Fatal("nil prm")
		}
	})
	t.Run("RejectsIncorrectResource", func(t *testing.T) {
		h := &fakeResourceHandler{resourceOverride: "https://attacker.com/evil"}
		server := httptest.NewTLSServer(h)
		h.installHandlers(server.URL)
		client := server.Client()
		metadataURL := server.URL + "/.well-known/oauth-protected-resource"
		prm, err := GetProtectedResourceMetadata(ctx, metadataURL, server.URL, client)
		if err == nil {
			t.Fatal("Expected validation error for mismatched resource, got nil")
		}
		if prm != nil {
			t.Fatal("Expected nil prm on validation failure")
		}
	})
}

type fakeResourceHandler struct {
	http.ServeMux
	resourceOverride string // If set, use this instead of correct resource (for testing validation)
}

func (h *fakeResourceHandler) installHandlers(serverURL string) {
	path := "/.well-known/oauth-protected-resource"
	h.Handle("GET "+path, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Per RFC 9728 section 3.3, the resource field should contain the actual resource identifier,
		// which is the URL the client uses to access the resource (serverURL + "/resource" for WWW-Authenticate case).
		// For the well-known URL test case, it's just the serverURL.
		resource := serverURL
		// Allow testing with custom resource values (e.g., impersonation attacks).
		if h.resourceOverride != "" {
			resource = h.resourceOverride
		}
		prm := &ProtectedResourceMetadata{Resource: resource}
		if err := json.NewEncoder(w).Encode(prm); err != nil {
			panic(err)
		}
	}))
}

// RFC 9110 section 11.2 allows optional whitespace (BWS) around the "=" of an
// auth-param, so "scope = ..." after a comma is a parameter, not a new
// challenge.
func TestParseWWWAuthenticateWhitespaceAroundEquals(t *testing.T) {
	tests := []struct {
		name   string
		header string
		want   []Challenge
	}{
		{
			name:   "space before and after",
			header: `Bearer realm="mcp", scope = "files:read"`,
			want: []Challenge{
				{Scheme: "bearer", Params: map[string]string{"realm": "mcp", "scope": "files:read"}},
			},
		},
		{
			name:   "space on one side only",
			header: `Bearer error="insufficient_scope", scope ="files:write", resource_metadata= "https://example.com/rm"`,
			want: []Challenge{
				{Scheme: "bearer", Params: map[string]string{
					"error":             "insufficient_scope",
					"scope":             "files:write",
					"resource_metadata": "https://example.com/rm",
				}},
			},
		},
		{
			name:   "tab before",
			header: "Bearer realm=\"mcp\", scope\t= \"files:read\"",
			want: []Challenge{
				{Scheme: "bearer", Params: map[string]string{"realm": "mcp", "scope": "files:read"}},
			},
		},
		{
			name:   "next challenge still splits",
			header: `Bearer scope = "files:read", Basic realm = "mcp"`,
			want: []Challenge{
				{Scheme: "bearer", Params: map[string]string{"scope": "files:read"}},
				{Scheme: "basic", Params: map[string]string{"realm": "mcp"}},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseWWWAuthenticate([]string{tt.header})
			if err != nil {
				t.Fatalf("ParseWWWAuthenticate(%q) error = %v", tt.header, err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ParseWWWAuthenticate(%q) = %+v, want %+v", tt.header, got, tt.want)
			}
		})
	}
}
