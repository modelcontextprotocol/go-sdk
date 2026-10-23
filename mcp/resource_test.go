// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package mcp

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestFileRoot(t *testing.T) {
	type test struct {
		uri     string
		want    string
		wantErr string // error must contain this string
	}
	tests := []test{
		{uri: "http:///foo", wantErr: "not a file"},
		{uri: "file://foo", wantErr: "empty path"},
		{uri: ":", wantErr: "missing protocol scheme"},
	}
	if runtime.GOOS == "windows" {
		tests = append(tests,
			test{uri: "file:///C:/foo", want: `C:\foo`},
			test{uri: "file:///c:/foo/bar", want: `c:\foo\bar`},
			test{uri: "file:///C:/foo/../bar", want: `C:\bar`},
			test{uri: "file:/C:/foo", want: `C:\foo`},
			// VS Code percent-encodes the colon after the drive letter.
			test{uri: "file:///c%3A/foo", want: `c:\foo`},
			test{uri: "file:///foo", wantErr: "not an absolute path"},
		)
	} else {
		tests = append(tests,
			test{uri: "file:///foo", want: "/foo"},
			test{uri: "file:///foo/bar", want: "/foo/bar"},
			test{uri: "file:///foo/../bar", want: "/bar"},
			test{uri: "file:/foo", want: "/foo"},
		)
	}
	for _, tt := range tests {
		got, err := fileRoot(&Root{URI: tt.uri})
		if err != nil {
			if tt.wantErr == "" {
				t.Errorf("%s: got %v, want success", tt.uri, err)
				continue
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("%s: got %v, does not contain %q", tt.uri, err, tt.wantErr)
				continue
			}
		} else if tt.wantErr != "" {
			t.Errorf("%s: succeeded, but wanted error with %q", tt.uri, tt.wantErr)
		} else if got != tt.want {
			t.Errorf("%s: got %q, want %q", tt.uri, got, tt.want)
		}
	}
}

func TestFileURIPath(t *testing.T) {
	for _, tt := range []struct {
		uriPath, goos, want string
	}{
		{"/C:/foo", "windows", "C:/foo"},
		{"/c:/foo", "windows", "c:/foo"},
		{"/C:", "windows", "C:"},
		{"/foo", "windows", "/foo"},
		{"/1:/foo", "windows", "/1:/foo"},
		{"/C:/foo", "linux", "/C:/foo"},
		{"/foo", "linux", "/foo"},
	} {
		if got := fileURIPath(tt.uriPath, tt.goos); got != tt.want {
			t.Errorf("fileURIPath(%q, %q) = %q, want %q", tt.uriPath, tt.goos, got, tt.want)
		}
	}
}

func TestComputeURIFilepath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("TODO: fix for Windows")
	}
	// TODO(jba): test with Windows \\host paths and C: paths
	dirFilepath := filepath.FromSlash("/files")
	rootFilepaths := []string{
		filepath.FromSlash("/files/public"),
		filepath.FromSlash("/files/shared"),
	}
	for _, tt := range []struct {
		uri     string
		want    string
		wantErr string // error must contain this
	}{
		{"file:///public", "public", ""},
		{"file:///public/file", "public/file", ""},
		{"file:///shared/file", "shared/file", ""},
		{"http:///foo", "", "not a file"},
		{"file://foo", "", "empty"},
		{"file://foo/../bar", "", "localized"},
		{"file:///secret", "", "root"},
		{"file:///secret/file", "", "root"},
		{"file:///private/file", "", "root"},
	} {
		t.Run(tt.uri, func(t *testing.T) {
			tt.want = filepath.FromSlash(tt.want) // handle Windows
			got, gotErr := computeURIFilepath(tt.uri, dirFilepath, rootFilepaths)
			if gotErr != nil {
				if tt.wantErr == "" {
					t.Fatalf("got %v, wanted success", gotErr)
				}
				if !strings.Contains(gotErr.Error(), tt.wantErr) {
					t.Fatalf("got error %v, does not contain %q", gotErr, tt.wantErr)
				}
				return
			}
			if tt.wantErr != "" {
				t.Fatal("succeeded unexpectedly")
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestReadFileResource(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("TODO: fix for Windows")
	}
	abs, err := filepath.Abs("testdata")
	if err != nil {
		t.Fatal(err)
	}
	dirFilepath := filepath.Join(abs, "files")
	got, err := readFileResource("file:///info.txt", dirFilepath, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := "Contents\n"
	if g := string(got); g != want {
		t.Errorf("got %q, want %q", g, want)
	}
}

func TestTemplateMatch(t *testing.T) {
	uri := "file:///path/to/file"
	for _, tt := range []struct {
		template string
		want     bool
	}{
		{"file:///{}/{a}/{b}", false}, // invalid: empty variable expression "{}" is not allowed in RFC 6570
		{"file:///{a}/{b}", false},
		{"file:///{+path}", true},
		{"file:///{a}/{+path}", true},
	} {
		resourceTmpl := serverResourceTemplate{resourceTemplate: &ResourceTemplate{URITemplate: tt.template}}
		if matched := resourceTmpl.Matches(uri); matched != tt.want {
			t.Errorf("%s: got %t, want %t", tt.template, matched, tt.want)
		}
	}
}

// TestComputeURIFilepathLocalizeError checks that a path which fails
// filepath.Localize (e.g. one escaping its directory) is named in the returned
// error. Regression: the message formatted filepath.Localize's error-path
// return value, which is always "", so it never identified the offending path.
func TestComputeURIFilepathLocalizeError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("TODO: fix for Windows")
	}
	dirFilepath := filepath.FromSlash("/files")
	_, err := computeURIFilepath("file://foo/../bar", dirFilepath, nil)
	if err == nil {
		t.Fatal("expected a localize error, got nil")
	}
	if !strings.Contains(err.Error(), "bar") {
		t.Errorf("error %q does not name the offending path %q", err, "../bar")
	}
}
