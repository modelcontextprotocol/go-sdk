// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package mcp

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/google/jsonschema-go/jsonschema"
)

type Item struct {
	Name  string
	Value string
}

type ListTestParams struct {
	Cursor string
}

func (p *ListTestParams) cursorPtr() *string {
	return &p.Cursor
}

type ListTestResult struct {
	Items      []*Item
	NextCursor string
}

func (r *ListTestResult) nextCursorPtr() *string {
	return &r.NextCursor
}

var allItems = []*Item{
	{"alpha", "val-A"},
	{"bravo", "val-B"},
	{"charlie", "val-C"},
	{"delta", "val-D"},
	{"echo", "val-E"},
	{"foxtrot", "val-F"},
	{"golf", "val-G"},
	{"hotel", "val-H"},
	{"india", "val-I"},
	{"juliet", "val-J"},
	{"kilo", "val-K"},
}

// generatePaginatedResults is a helper to create a sequence of mock responses for pagination.
// It simulates a server returning items in pages based on a given page size.
func generatePaginatedResults(all []*Item, pageSize int) []*ListTestResult {
	if len(all) == 0 {
		return []*ListTestResult{{Items: []*Item{}, NextCursor: ""}}
	}
	if pageSize <= 0 {
		panic("pageSize must be greater than 0")
	}
	numPages := (len(all) + pageSize - 1) / pageSize // Ceiling division
	var results []*ListTestResult
	for i := range numPages {
		startIndex := i * pageSize
		endIndex := min(startIndex+pageSize, len(all)) // Use min to prevent out of bounds
		nextCursor := ""
		if endIndex < len(all) { // If there are more items after this page
			nextCursor = fmt.Sprintf("cursor_%d", endIndex)
		}
		results = append(results, &ListTestResult{Items: all[startIndex:endIndex], NextCursor: nextCursor})
	}
	return results
}

func TestClientPaginateBasic(t *testing.T) {
	ctx := context.Background()
	testCases := []struct {
		name          string
		results       []*ListTestResult
		mockError     error
		initialParams *ListTestParams
		expected      []*Item
		expectError   bool
	}{
		{
			name:     "SinglePageAllItems",
			results:  generatePaginatedResults(allItems, len(allItems)),
			expected: allItems,
		},
		{
			name:     "MultiplePages",
			results:  generatePaginatedResults(allItems, 3),
			expected: allItems,
		},
		{
			name:     "EmptyResults",
			results:  generatePaginatedResults([]*Item{}, 10),
			expected: nil,
		},
		{
			name:        "ListFuncReturnsErrorImmediately",
			results:     []*ListTestResult{{}},
			mockError:   fmt.Errorf("API error on first call"),
			expected:    nil,
			expectError: true,
		},
		{
			name:          "InitialCursorProvided",
			initialParams: &ListTestParams{Cursor: "cursor_2"},
			results:       generatePaginatedResults(allItems[2:], 3),
			expected:      allItems[2:],
		},
		{
			name:          "CursorBeyondAllItems",
			initialParams: &ListTestParams{Cursor: "cursor_999"},
			results:       []*ListTestResult{{Items: []*Item{}, NextCursor: ""}},
			expected:      nil,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			listFunc := func(ctx context.Context, params *ListTestParams) (*ListTestResult, error) {
				if len(tc.results) == 0 {
					t.Fatalf("listFunc called but no more results defined for test case %q", tc.name)
				}
				res := tc.results[0]
				tc.results = tc.results[1:]
				var err error
				if tc.mockError != nil {
					err = tc.mockError
				}
				return res, err
			}

			params := tc.initialParams
			if tc.initialParams == nil {
				params = &ListTestParams{}
			}

			var gotItems []*Item
			var iterationErr error
			seq := paginate(ctx, params, listFunc, func(r *ListTestResult) []*Item { return r.Items })
			for item, err := range seq {
				if err != nil {
					iterationErr = err
					break
				}
				gotItems = append(gotItems, item)
			}
			if tc.expectError {
				if iterationErr == nil {
					t.Errorf("paginate() expected an error during iteration, but got none")
				}
			} else {
				if iterationErr != nil {
					t.Errorf("paginate() got: %v, want: nil", iterationErr)
				}
			}
			if diff := cmp.Diff(tc.expected, gotItems, cmpopts.IgnoreUnexported(jsonschema.Schema{})); diff != "" {
				t.Fatalf("paginate() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestClientPaginateVariousPageSizes(t *testing.T) {
	ctx := context.Background()
	for i := 1; i < len(allItems)+1; i++ {
		testname := fmt.Sprintf("PageSize=%d", i)
		t.Run(testname, func(t *testing.T) {
			results := generatePaginatedResults(allItems, i)
			listFunc := func(ctx context.Context, params *ListTestParams) (*ListTestResult, error) {
				res := results[0]
				results = results[1:]
				return res, nil
			}
			var gotItems []*Item
			seq := paginate(ctx, &ListTestParams{}, listFunc, func(r *ListTestResult) []*Item { return r.Items })
			for item, err := range seq {
				if err != nil {
					t.Fatalf("paginate() unexpected error during iteration: %v", err)
				}
				gotItems = append(gotItems, item)
			}
			if diff := cmp.Diff(allItems, gotItems, cmpopts.IgnoreUnexported(jsonschema.Schema{})); diff != "" {
				t.Fatalf("paginate() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestClientCapabilities(t *testing.T) {
	testCases := []struct {
		name             string
		configureClient  func(s *Client)
		clientOpts       ClientOptions
		protocolVersion  string // defaults to latestProtocolVersion if empty
		wantCapabilities *ClientCapabilities
	}{
		{
			name:            "default",
			configureClient: func(s *Client) {},
			wantCapabilities: &ClientCapabilities{
				Roots:   RootCapabilities{ListChanged: true},
				RootsV2: &RootCapabilities{ListChanged: true},
			},
		},
		{
			name:            "with sampling",
			configureClient: func(s *Client) {},
			clientOpts: ClientOptions{
				CreateMessageHandler: func(context.Context, *CreateMessageRequest) (*CreateMessageResult, error) {
					return nil, nil
				},
			},
			wantCapabilities: &ClientCapabilities{
				Roots:    RootCapabilities{ListChanged: true},
				RootsV2:  &RootCapabilities{ListChanged: true},
				Sampling: &SamplingCapabilities{},
			},
		},
		{
			name:            "with elicitation",
			configureClient: func(s *Client) {},
			clientOpts: ClientOptions{
				ElicitationHandler: func(context.Context, *ElicitRequest) (*ElicitResult, error) {
					return nil, nil
				},
			},
			protocolVersion: protocolVersion20251125,
			wantCapabilities: &ClientCapabilities{
				Roots:   RootCapabilities{ListChanged: true},
				RootsV2: &RootCapabilities{ListChanged: true},
				Elicitation: &ElicitationCapabilities{
					Form: &FormElicitationCapabilities{},
				},
			},
		},
		{
			name:            "with elicitation (old protocol)",
			configureClient: func(s *Client) {},
			clientOpts: ClientOptions{
				ElicitationHandler: func(context.Context, *ElicitRequest) (*ElicitResult, error) {
					return nil, nil
				},
			},
			protocolVersion: protocolVersion20250618,
			wantCapabilities: &ClientCapabilities{
				Roots:       RootCapabilities{ListChanged: true},
				RootsV2:     &RootCapabilities{ListChanged: true},
				Elicitation: &ElicitationCapabilities{},
			},
		},
		{
			name:            "with URL elicitation",
			configureClient: func(s *Client) {},
			clientOpts: ClientOptions{
				Capabilities: &ClientCapabilities{
					Roots:   RootCapabilities{ListChanged: true},
					RootsV2: &RootCapabilities{ListChanged: true},
					Elicitation: &ElicitationCapabilities{
						URL: &URLElicitationCapabilities{},
					},
				},
				ElicitationHandler: func(context.Context, *ElicitRequest) (*ElicitResult, error) {
					return nil, nil
				},
			},
			wantCapabilities: &ClientCapabilities{
				Roots:   RootCapabilities{ListChanged: true},
				RootsV2: &RootCapabilities{ListChanged: true},
				Elicitation: &ElicitationCapabilities{
					URL: &URLElicitationCapabilities{},
				},
			},
		},
		{
			name:            "with form and URL elicitation",
			configureClient: func(s *Client) {},
			clientOpts: ClientOptions{
				Capabilities: &ClientCapabilities{
					Roots:   RootCapabilities{ListChanged: true},
					RootsV2: &RootCapabilities{ListChanged: true},
					Elicitation: &ElicitationCapabilities{
						Form: &FormElicitationCapabilities{},
						URL:  &URLElicitationCapabilities{},
					},
				},
				ElicitationHandler: func(context.Context, *ElicitRequest) (*ElicitResult, error) {
					return nil, nil
				},
			},
			wantCapabilities: &ClientCapabilities{
				Roots:   RootCapabilities{ListChanged: true},
				RootsV2: &RootCapabilities{ListChanged: true},
				Elicitation: &ElicitationCapabilities{
					Form: &FormElicitationCapabilities{},
					URL:  &URLElicitationCapabilities{},
				},
			},
		},
		{
			name:            "no capabilities",
			configureClient: func(s *Client) {},
			clientOpts: ClientOptions{
				Capabilities: &ClientCapabilities{},
			},
			wantCapabilities: &ClientCapabilities{},
		},
		{
			name:            "no roots",
			configureClient: func(s *Client) {},
			clientOpts: ClientOptions{
				Capabilities: &ClientCapabilities{
					Sampling: &SamplingCapabilities{},
				},
			},
			wantCapabilities: &ClientCapabilities{
				Sampling: &SamplingCapabilities{},
			},
		},
		{
			name:            "roots-no list",
			configureClient: func(s *Client) {},
			clientOpts: ClientOptions{
				Capabilities: &ClientCapabilities{
					RootsV2: &RootCapabilities{ListChanged: false},
				},
			},
			wantCapabilities: &ClientCapabilities{
				RootsV2: &RootCapabilities{ListChanged: false},
			},
		},
		{
			name:            "custom capabilities with sampling",
			configureClient: func(s *Client) {},
			clientOpts: ClientOptions{
				Capabilities: &ClientCapabilities{
					RootsV2: &RootCapabilities{ListChanged: true},
				},
				CreateMessageHandler: func(context.Context, *CreateMessageRequest) (*CreateMessageResult, error) {
					return nil, nil
				},
			},
			wantCapabilities: &ClientCapabilities{
				Roots:    RootCapabilities{ListChanged: true},
				RootsV2:  &RootCapabilities{ListChanged: true},
				Sampling: &SamplingCapabilities{},
			},
		},
		{
			name:            "elicitation override",
			configureClient: func(s *Client) {},
			clientOpts: ClientOptions{
				Capabilities: &ClientCapabilities{
					Elicitation: &ElicitationCapabilities{
						URL: &URLElicitationCapabilities{},
					},
				},
				ElicitationHandler: func(context.Context, *ElicitRequest) (*ElicitResult, error) {
					return nil, nil
				},
			},
			wantCapabilities: &ClientCapabilities{
				Elicitation: &ElicitationCapabilities{
					URL: &URLElicitationCapabilities{},
				},
			},
		},
		{
			name:            "custom capabilities with experimental",
			configureClient: func(s *Client) {},
			clientOpts: ClientOptions{
				Capabilities: &ClientCapabilities{
					Experimental: map[string]any{"custom": "value"},
					RootsV2:      &RootCapabilities{ListChanged: true},
				},
			},
			wantCapabilities: &ClientCapabilities{
				Experimental: map[string]any{"custom": "value"},
				Roots:        RootCapabilities{ListChanged: true},
				RootsV2:      &RootCapabilities{ListChanged: true},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			client := NewClient(testImpl, &tc.clientOpts)
			tc.configureClient(client)
			protocolVersion := tc.protocolVersion
			if protocolVersion == "" {
				protocolVersion = latestProtocolVersion
			}
			gotCapabilities := client.capabilities(protocolVersion)
			if diff := cmp.Diff(tc.wantCapabilities, gotCapabilities); diff != "" {
				t.Errorf("capabilities() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestClientCapabilitiesOverWire(t *testing.T) {
	testCases := []struct {
		name             string
		clientOpts       *ClientOptions
		wantCapabilities *ClientCapabilities
	}{
		{
			name:       "Default capabilities",
			clientOpts: nil,
			wantCapabilities: &ClientCapabilities{
				Roots:   RootCapabilities{ListChanged: true},
				RootsV2: &RootCapabilities{ListChanged: true},
			},
		},
		{
			name: "Custom Capabilities with roots listChanged false",
			clientOpts: &ClientOptions{
				Capabilities: &ClientCapabilities{
					RootsV2: &RootCapabilities{ListChanged: false},
				},
			},
			wantCapabilities: &ClientCapabilities{
				Roots:   RootCapabilities{ListChanged: false},
				RootsV2: &RootCapabilities{ListChanged: false},
			},
		},
		{
			name: "Dynamic sampling capability",
			clientOpts: &ClientOptions{
				Capabilities: &ClientCapabilities{
					RootsV2: &RootCapabilities{ListChanged: true},
				},
				CreateMessageHandler: func(context.Context, *CreateMessageRequest) (*CreateMessageResult, error) {
					return nil, nil
				},
			},
			wantCapabilities: &ClientCapabilities{
				Roots:    RootCapabilities{ListChanged: true},
				RootsV2:  &RootCapabilities{ListChanged: true},
				Sampling: &SamplingCapabilities{},
			},
		},
		{
			name: "Empty capabilities disables defaults",
			clientOpts: &ClientOptions{
				Capabilities: &ClientCapabilities{},
			},
			wantCapabilities: &ClientCapabilities{},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()

			// Create client.
			impl := &Implementation{Name: "testClient", Version: "v1.0.0"}
			client := NewClient(impl, tc.clientOpts)

			// Connect client and server.
			cTransport, sTransport := NewInMemoryTransports()
			server := NewServer(&Implementation{Name: "testServer", Version: "v1.0.0"}, nil)
			ss, err := server.Connect(ctx, sTransport, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer ss.Close()

			cs, err := client.Connect(ctx, cTransport, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer cs.Close()

			// Check that the server received the expected capabilities.
			initParams := ss.InitializeParams()
			if initParams == nil {
				t.Fatal("InitializeParams is nil")
			}

			if diff := cmp.Diff(tc.wantCapabilities, initParams.Capabilities); diff != "" {
				t.Errorf("Capabilities mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
