// Copyright 2026 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by the license
// that can be found in the LICENSE file.

package mcp_test

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestStdioServerDoesNotLinkTLS builds a stdio-only server and checks that
// the linker drops the TLS client. A package-level initializer that touches
// http.Transport would pull it back in for every program importing mcp.
func TestStdioServerDoesNotLinkTLS(t *testing.T) {
	goTool, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go tool not found")
	}
	bin := filepath.Join(t.TempDir(), "hello")
	if out, err := exec.Command(goTool, "build", "-o", bin, "../examples/server/hello").CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	out, err := exec.Command(goTool, "tool", "nm", bin).CombinedOutput()
	if err != nil {
		t.Fatalf("go tool nm: %v\n%s", err, out)
	}
	wantSym := map[string]bool{
		// Sanity check that nm read the server binary.
		"github.com/modelcontextprotocol/go-sdk/mcp.(*Server).Run": true,
		// The exported Handshake is inlined away, so check its callee.
		"crypto/tls.(*Conn).clientHandshake": false,
	}
	for sym, want := range wantSym {
		got := strings.Contains(string(out), sym)
		if want && !got {
			t.Errorf("symbol %s not found; is this the right binary?", sym)
		}
		if !want && got {
			t.Errorf("stdio-only server links %s; a package initializer likely reaches http.Transport (see go build -ldflags=-dumpdep)", sym)
		}
	}
}
