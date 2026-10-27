// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package docs

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Headings are slugged twice by two different tools: weave writes the %toc
// links, and the renderer (GitHub, and mkdocs on go.sdk.modelcontextprotocol.io)
// assigns the heading ids. The two agree on plain words, and disagree on
// punctuation: weave keeps "(", ")" and "/" and drops "_", the renderers drop
// the first three and keep the last. A heading carrying any of them therefore
// gets a table-of-contents entry that lands nowhere.
//
// This test reads the generated docs and checks every %toc entry against the
// ids the renderers will actually produce.

var (
	headingRE = regexp.MustCompile(`(?m)^(#{1,6})\s+(.*?)\s*$`)
	tocLineRE = regexp.MustCompile(`(?m)^\s*1\. \[(.*?)\]\((#.*?)\)\s*$`)
	dropRE    = regexp.MustCompile(`[^\w\s-]`)
	spaceRE   = regexp.MustCompile(`[-\s]+`)
)

// renderedID reproduces the anchor GitHub and python-markdown's toc extension
// derive from heading text: take the text, drop everything that is not a word
// character, whitespace or a hyphen, lowercase it, and join on hyphens.
func renderedID(heading string) string {
	s := strings.ReplaceAll(heading, "`", "")
	s = dropRE.ReplaceAllString(s, "")
	s = strings.ToLower(strings.TrimSpace(s))
	return spaceRE.ReplaceAllString(s, "-")
}

func TestTOCAnchorsResolve(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("..", "..", "docs", "*.md"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no generated docs found")
	}
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		text := string(data)

		ids := make(map[string]bool)
		for _, m := range headingRE.FindAllStringSubmatch(text, -1) {
			ids[renderedID(m[2])] = true
		}
		for _, m := range tocLineRE.FindAllStringSubmatch(text, -1) {
			label, target := m[1], strings.TrimPrefix(m[2], "#")
			if !ids[target] {
				t.Errorf("%s: table-of-contents entry %q points at #%s, which no heading produces "+
					"(want #%s); drop (, ), / and _ from the heading",
					filepath.Base(file), label, target, renderedID(label))
			}
		}
	}
}
