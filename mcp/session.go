// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package mcp

// hasSessionID is the interface which, if implemented by connections, informs
// the session about their session ID.
//
// TODO(rfindley): remove SessionID methods from connections, when it doesn't
// make sense. Or remove it from the Sessions entirely: why does it even need
// to be exposed?
type hasSessionID interface {
	SessionID() string
}

// ServerSessionState is the state of a session.
type ServerSessionState struct {
	// InitializeParams are the parameters from 'initialize'.
	InitializeParams *InitializeParams `json:"initializeParams"`

	// InitializedParams are the parameters from 'notifications/initialized'.
	InitializedParams *InitializedParams `json:"initializedParams"`

	// NegotiatedProtocolVersion is the protocol version the session speaks.
	//
	// The initialize handshake sets it to the version it settled on, which
	// differs from the one the client requested when the server does not
	// support that one. A session that runs no handshake records the version
	// its first new-protocol request declared, once the server has accepted
	// it (SEP-2575); the version 'server/discover' was asked about, when the
	// answer lists it; or the MCP-Protocol-Version header of a request served
	// without a handshake.
	//
	// It is empty for a session that has recorded no version yet, and in
	// state written before the SDK recorded it outside the handshake, where
	// InitializeParams.ProtocolVersion is the version the session speaks.
	NegotiatedProtocolVersion string `json:"negotiatedProtocolVersion,omitempty"`

	// LogLevel is the logging level for the session.
	LogLevel LoggingLevel `json:"logLevel"`

	// TODO: resource subscriptions
}
