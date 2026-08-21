// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

// Package latinext is an example MCP extension that adds a "latin/translate"
// custom JSON-RPC method. It demonstrates the extension-author pattern: types,
// the CustomMethod variable, and the init() registration are all defined here
// so that importers get everything wired up automatically.
package latinext

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// TranslateParams are the parameters for the latin/translate method.
type TranslateParams struct {
	mcp.ParamsBase
	Text string `json:"text"`
}

// TranslateResult is the result of the latin/translate method.
type TranslateResult struct {
	mcp.ResultBase
	Latin string `json:"latin"`
}

// Method captures the method name and types once. Extension consumers call
// Translate() rather than this directly.
var Method = mcp.NewCustomMethod[*TranslateParams, *TranslateResult]("latin/translate")

func init() {
	mcp.RegisterExtension(mcp.Extension{
		Server: func(s *mcp.Server) error {
			return Method.RegisterServerReceiving(s, DefaultHandler)
		},
		Client: func(c *mcp.Client) error {
			return Method.RegisterClientSending(c)
		},
	})
}

// Translate calls the latin/translate method on the server via cs.
// This is the one-liner that extension consumers use — no generics, no method
// name strings.
func Translate(ctx context.Context, cs *mcp.ClientSession, text string) (*TranslateResult, error) {
	return Method.Call(ctx, cs, &TranslateParams{Text: text})
}

// DefaultHandler is the reference server-side implementation. It can be
// overridden per-server by calling Method.RegisterServer(server, myHandler).
func DefaultHandler(_ context.Context, _ *mcp.ServerSession, params *TranslateParams) (*TranslateResult, error) {
	key := strings.ToLower(strings.TrimSpace(params.Text))
	latin, ok := translations[key]
	if !ok {
		latin = fmt.Sprintf("[unknown: %q]", params.Text)
	}
	return &TranslateResult{Latin: latin}, nil
}

var translations = map[string]string{
	"hello":                    "salve",
	"goodbye":                  "vale",
	"thank you":                "gratias tibi ago",
	"how are you":              "quid agis",
	"good morning":             "bonum mane",
	"good night":               "bonam noctem",
	"friend":                   "amicus",
	"water":                    "aqua",
	"love":                     "amor",
	"war":                      "bellum",
	"peace":                    "pax",
	"truth":                    "veritas",
	"light":                    "lux",
	"time":                     "tempus",
	"life":                     "vita",
	"death":                    "mors",
	"star":                     "stella",
	"earth":                    "terra",
	"sea":                      "mare",
	"the die is cast":          "alea iacta est",
	"i came i saw i conquered": "veni vidi vici",
	"seize the day":            "carpe diem",
}
