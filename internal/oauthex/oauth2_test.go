// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package oauthex

import (
	"context"
	"encoding/json"
	"fmt"
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
			name:  "empty input",
			input: "",
			want:  []string{""},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := splitChallenges(tt.input)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("splitChallenges() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParseSingleChallenge(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    challenge
		wantErr bool
	}{
		{
			name:  "scheme only",
			input: "Basic",
			want: challenge{
				Scheme: "basic",
			},
			wantErr: false,
		},
		{
			name:  "scheme with one quoted param",
			input: `Bearer realm="example.com"`,
			want: challenge{
				Scheme: "bearer",
				Params: map[string]string{"realm": "example.com"},
			},
			wantErr: false,
		},
		{
			name:  "scheme with multiple params",
			input: `Bearer realm="example", error="invalid_token", error_description="The token expired"`,
			want: challenge{
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
			want: challenge{
				Scheme: "bearer",
				Params: map[string]string{"realm": "example"},
			},
			wantErr: false,
		},
		{
			name:  "param with escaped quote",
			input: `Bearer realm="example \"foo\" bar"`,
			want: challenge{
				Scheme: "bearer",
				Params: map[string]string{"realm": `example "foo" bar`},
			},
			wantErr: false,
		},
		{
			name:  "param without quotes (token)",
			input: "Bearer realm=example.com",
			want: challenge{
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

func TestGetProtectedResourceMetadataFromRequest(t *testing.T) {
	h := &fakeResourceHandler{}
	server := httptest.NewServer(h)
	h.installHandlers(server.URL)
	client := server.Client()
	res, err := http.Get(server.URL + "/resource")
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatal("want unauth")
	}
	prm, err := GetProtectedResourceMetadataFromHeader(context.Background(), res.Header, client)
	if err != nil {
		t.Fatal(err)
	}
	if prm == nil {
		t.Fatal("nil prm")
	}
}

type fakeResourceHandler struct {
	http.ServeMux
	serverURL     string
	authenticated bool
}

func (h *fakeResourceHandler) installHandlers(serverURL string) {
	path := "/.well-known/oauth-protected-resource"
	url := serverURL + path
	h.Handle("GET /resource", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h.authenticated {
			fmt.Fprintln(w, "resource")
			return
		}
		w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Bearer resource_metadata="%s"`, url))
		w.WriteHeader(http.StatusUnauthorized)
	}))
	h.Handle("GET "+path, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		prm := &ProtectedResourceMetadata{Resource: url}
		if err := json.NewEncoder(w).Encode(prm); err != nil {
			panic(err)
		}
	}))
}
