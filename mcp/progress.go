// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package mcp

import (
	"context"
	"errors"
)

var ErrNoProgressToken = errors.New("no progress token")

// Progress reports progress on the current request.
//
// An error is returned if sending progress failed. If there was no progress
// token, this error is ErrNoProgressToken.
func (r *ServerRequest[P]) Progress(ctx context.Context, msg string, progress, total float64) error {
	token, ok := r.Params.GetMeta()[progressTokenKey]
	if !ok {
		return ErrNoProgressToken
	}
	params := &ProgressNotificationParams{
		Message:       msg,
		ProgressToken: token,
		Progress:      progress,
		Total:         total,
	}
	return r.Session.NotifyProgress(ctx, params)
}
