// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

//go:build (go1.24 && goexperiment.synctest) || go1.25

package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/internal/jsonrpc2"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"golang.org/x/tools/txtar"
)

var update = flag.Bool("update", false, "if set, update conformance test data")

// A conformance test checks JSON-level conformance of a test server or client.
// This allows us to confirm that we can handle the input or output of other
// SDKs, even if they behave differently at the JSON level (for example, have
// different behavior with respect to optional fields).
//
// The client and server fields hold an encoded sequence of JSON-RPC messages.
//
// For server tests, the client messages are a sequence of messages to be sent
// from the (synthetic) client and the server messages are the expected
// messages to be received from the real server.
//
// For client tests, it's the other way around: server messages are synthetic,
// and client messages are expected from the real client.
//
// Conformance tests are loaded from txtar-encoded testdata files. Run the test
// with -update to have the test runner update the expected output, which may
// be client or server depending on the perspective of the test.
type conformanceTest struct {
	name                      string            // test name
	path                      string            // path to test file
	archive                   *txtar.Archive    // raw archive, for updating
	tools, prompts, resources []string          // named features to include
	client                    []jsonrpc.Message // client messages
	server                    []jsonrpc.Message // server messages
}

// TODO(rfindley): add client conformance tests.

func TestServerConformance(t *testing.T) {
	var tests []*conformanceTest
	dir := filepath.Join("testdata", "conformance", "server")
	if err := filepath.WalkDir(dir, func(path string, _ fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if strings.HasSuffix(path, ".txtar") {
			test, err := loadConformanceTest(dir, path)
			if err != nil {
				return fmt.Errorf("%s: %v", path, err)
			}
			tests = append(tests, test)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// We use synctest here because in general, there is no way to know when the
			// server is done processing any notifications. As long as our server doesn't
			// do background work, synctest provides an easy way for us to detect when the
			// server is done processing.
			//
			// By comparison, gopls has a complicated framework based on progress
			// reporting and careful accounting to detect when all 'expected' work
			// on the server is complete.
			runSyncTest(t, func(t *testing.T) { runServerTest(t, test) })

			// TODO: in 1.25, use the following instead:
			// synctest.Test(t, func(t *testing.T) {
			// 	runServerTest(t, test)
			// })
		})
	}
}

type structuredInput struct {
	In string `jsonschema:"the input"`
}

type structuredOutput struct {
	Out string `jsonschema:"the output"`
}

func structuredTool(ctx context.Context, req *CallToolRequest, args *structuredInput) (*CallToolResult, *structuredOutput, error) {
	return nil, &structuredOutput{"Ack " + args.In}, nil
}

type tomorrowInput struct {
	Now time.Time
}

type tomorrowOutput struct {
	Tomorrow time.Time
}

func tomorrowTool(ctx context.Context, req *CallToolRequest, args tomorrowInput) (*CallToolResult, tomorrowOutput, error) {
	return nil, tomorrowOutput{args.Now.Add(24 * time.Hour)}, nil
}

type incInput struct {
	X int `json:"x,omitempty"`
}

type incOutput struct {
	Y int `json:"y"`
}

func incTool(_ context.Context, _ *CallToolRequest, args incInput) (*CallToolResult, incOutput, error) {
	return nil, incOutput{args.X + 1}, nil
}

var iconObj = Icon{Source: "data:image/png;base64,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",
	MIMEType: "image/png", Sizes: []string{"48x48", "96x96"}}

// runServerTest runs the server conformance test.
// It must be executed in a synctest bubble.
func runServerTest(t *testing.T, test *conformanceTest) {
	ctx := t.Context()
	// Construct the server based on features listed in the test.
	impl := &Implementation{Name: "testServer", Version: "v1.0.0"}

	// TODO(IAmSurajBobade): Remove this hack once we have a client protocol specific handling.
	if test.name == "version-draft.txtar" {
		impl.Icons = []Icon{iconObj}
		impl.WebsiteURL = "https://modelcontextprotocol.io"
	}

	s := NewServer(impl, nil)
	for _, tn := range test.tools {
		switch tn {
		case "greet":
			AddTool(s, &Tool{
				Name:        "greet",
				Description: "say hi",
			}, sayHi)
		case "greetWithIcon":
			AddTool(s, &Tool{
				Name:        "greetWithIcon",
				Description: "say hi",
				Icons:       []Icon{iconObj},
			}, sayHi)
		case "structured":
			AddTool(s, &Tool{Name: "structured"}, structuredTool)
		case "tomorrow":
			AddTool(s, &Tool{Name: "tomorrow"}, tomorrowTool)
		case "inc":
			inSchema, err := jsonschema.For[incInput](nil)
			if err != nil {
				t.Fatal(err)
			}
			inSchema.Properties["x"].Default = json.RawMessage(`6`)
			AddTool(s, &Tool{Name: "inc", InputSchema: inSchema}, incTool)
		default:
			t.Fatalf("unknown tool %q", tn)
		}
	}
	for _, pn := range test.prompts {
		switch pn {
		case "code_review":
			s.AddPrompt(codeReviewPrompt, codReviewPromptHandler)
		case "code_reviewWithIcon":
			s.AddPrompt(&Prompt{
				Name:        "code_review",
				Description: "do a code review",
				Arguments:   []*PromptArgument{{Name: "Code", Required: true}},
				Icons:       []Icon{iconObj},
			}, codReviewPromptHandler)
		default:
			t.Fatalf("unknown prompt %q", pn)
		}
	}
	for _, rn := range test.resources {
		switch rn {
		case "info.txt":
			s.AddResource(resource1, readHandler)
		case "info":
			s.AddResource(resource3, handleEmbeddedResource)
		case "infoWithIcon":
			s.AddResource(&Resource{
				Name:     "info",
				MIMEType: "text/plain",
				URI:      "embedded:info",
				Icons:    []Icon{iconObj},
			}, handleEmbeddedResource)
		default:
			t.Fatalf("unknown resource %q", rn)
		}
	}

	// Connect the server, and connect the client stream,
	// but don't connect an actual client.
	cTransport, sTransport := NewInMemoryTransports()
	ss, err := s.Connect(ctx, sTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	cStream, err := cTransport.Connect(ctx)
	if err != nil {
		t.Fatal(err)
	}

	writeMsg := func(msg jsonrpc.Message) {
		if err := cStream.Write(ctx, msg); err != nil {
			t.Fatalf("Write failed: %v", err)
		}
	}

	var (
		serverMessages []jsonrpc.Message
		outRequests    []*jsonrpc.Request
		outResponses   []*jsonrpc.Response
	)

	// Separate client requests and responses; we use them differently.
	for _, msg := range test.client {
		switch msg := msg.(type) {
		case *jsonrpc.Request:
			outRequests = append(outRequests, msg)
		case *jsonrpc.Response:
			outResponses = append(outResponses, msg)
		default:
			t.Fatalf("bad message type %T", msg)
		}
	}

	// nextResponse handles incoming requests and notifications, and returns the
	// next incoming response.
	nextResponse := func() (*jsonrpc.Response, error, bool) {
		for {
			msg, err := cStream.Read(ctx)
			if err != nil {
				// TODO(rfindley): we don't document (or want to document) that the in
				// memory transports use a net.Pipe. How can users detect this failure?
				// Should we promote it to EOF?
				if errors.Is(err, io.ErrClosedPipe) {
					err = nil
				}
				return nil, err, false
			}
			serverMessages = append(serverMessages, msg)
			if req, ok := msg.(*jsonrpc.Request); ok && req.IsCall() {
				// Pair up the next outgoing response with this request.
				// We assume requests arrive in the same order every time.
				if len(outResponses) == 0 {
					t.Fatalf("no outgoing response for request %v", req)
				}
				outResponses[0].ID = req.ID
				writeMsg(outResponses[0])
				outResponses = outResponses[1:]
				continue
			}
			return msg.(*jsonrpc.Response), nil, true
		}
	}

	// Synthetic peer interacts with real peer.
	for _, req := range outRequests {
		writeMsg(req)
		if req.IsCall() {
			// A call (as opposed to a notification). Wait for the response.
			res, err, ok := nextResponse()
			if err != nil {
				t.Fatalf("reading server messages failed: %v", err)
			}
			if !ok {
				t.Fatalf("missing response for request %v", req)
			}
			if res.ID != req.ID {
				t.Fatalf("out-of-order response %v to request %v", req, res)
			}
		}
	}
	// There might be more notifications or requests, but there shouldn't be more
	// responses.
	// Run this in a goroutine so the current thread can wait for it.
	var extra *jsonrpc.Response
	go func() {
		extra, err, _ = nextResponse()
	}()
	// Before closing the stream, wait for all messages to be processed.
	synctest.Wait()
	if err != nil {
		t.Fatalf("reading server messages failedd: %v", err)
	}
	if extra != nil {
		t.Fatalf("got extra response: %v", extra)
	}
	if err := cStream.Close(); err != nil {
		t.Fatalf("Stream.Close failed: %v", err)
	}
	ss.Wait()

	// Handle server output. If -update is set, write the 'server' file.
	// Otherwise, compare with expected.
	if *update {
		arch := &txtar.Archive{
			Comment: test.archive.Comment,
		}
		var buf bytes.Buffer
		for _, msg := range serverMessages {
			data, err := jsonrpc2.EncodeIndent(msg, "", "\t")
			if err != nil {
				t.Fatalf("jsonrpc2.EncodeIndent failed: %v", err)
			}
			buf.Write(data)
			buf.WriteByte('\n')
		}
		serverFile := txtar.File{Name: "server", Data: buf.Bytes()}
		seenServer := false // replace or append the 'server' file
		for _, f := range test.archive.Files {
			if f.Name == "server" {
				seenServer = true
				arch.Files = append(arch.Files, serverFile)
			} else {
				arch.Files = append(arch.Files, f)
			}
		}
		if !seenServer {
			arch.Files = append(arch.Files, serverFile)
		}
		if err := os.WriteFile(test.path, txtar.Format(arch), 0o666); err != nil {
			t.Fatalf("os.WriteFile(%q) failed: %v", test.path, err)
		}
	} else {
		// jsonrpc.Messages are not comparable, so we instead compare lines of JSON.
		transform := cmpopts.AcyclicTransformer("toJSON", func(msg jsonrpc.Message) []string {
			encoded, err := jsonrpc2.EncodeIndent(msg, "", "\t")
			if err != nil {
				t.Fatal(err)
			}
			return strings.Split(string(encoded), "\n")
		})
		if diff := cmp.Diff(test.server, serverMessages, transform); diff != "" {
			t.Errorf("Mismatching server messages (-want +got):\n%s", diff)
		}
	}
}

// loadConformanceTest loads one conformance test from the given path contained
// in the root dir.
func loadConformanceTest(dir, path string) (*conformanceTest, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	test := &conformanceTest{
		name:    strings.TrimPrefix(path, dir+string(filepath.Separator)),
		path:    path,
		archive: txtar.Parse(content),
	}
	if len(test.archive.Files) == 0 {
		return nil, fmt.Errorf("txtar archive %q has no '-- filename --' sections", path)
	}

	// decodeMessages loads JSON-RPC messages from the archive file.
	decodeMessages := func(data []byte) ([]jsonrpc.Message, error) {
		dec := json.NewDecoder(bytes.NewReader(data))
		var res []jsonrpc.Message
		for dec.More() {
			var raw json.RawMessage
			if err := dec.Decode(&raw); err != nil {
				return nil, err
			}
			m, err := jsonrpc2.DecodeMessage(raw)
			if err != nil {
				return nil, err
			}
			res = append(res, m)
		}
		return res, nil
	}
	// loadFeatures loads lists of named features from the archive file.
	loadFeatures := func(data []byte) []string {
		var feats []string
		for line := range strings.Lines(string(data)) {
			if f := strings.TrimSpace(line); f != "" {
				feats = append(feats, f)
			}
		}
		return feats
	}

	seen := make(map[string]bool) // catch accidentally duplicate files
	for _, f := range test.archive.Files {
		if seen[f.Name] {
			return nil, fmt.Errorf("duplicate file name %q", f.Name)
		}
		seen[f.Name] = true
		switch f.Name {
		case "tools":
			test.tools = loadFeatures(f.Data)
		case "prompts":
			test.prompts = loadFeatures(f.Data)
		case "resources":
			test.resources = loadFeatures(f.Data)
		case "client":
			test.client, err = decodeMessages(f.Data)
			if err != nil {
				return nil, fmt.Errorf("txtar archive %q contains bad -- client -- section: %v", path, err)
			}
		case "server":
			test.server, err = decodeMessages(f.Data)
			if err != nil {
				return nil, fmt.Errorf("txtar archive %q contains bad -- server -- section: %v", path, err)
			}
		default:
			return nil, fmt.Errorf("txtar archive %q contains unexpected file %q", path, f.Name)
		}
	}

	return test, nil
}
