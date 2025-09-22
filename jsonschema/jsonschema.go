// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package jsonschema

import (
	"reflect"

	"github.com/google/jsonschema-go/jsonschema"
)

func Ptr[T any](x T) *T {
	return jsonschema.Ptr(x)
}

type ForOptions = jsonschema.ForOptions

type Resolved = jsonschema.Resolved

type ResolveOptions = jsonschema.ResolveOptions

type Schema = jsonschema.Schema

func For[T any](opts *ForOptions) (*Schema, error) {
	return jsonschema.For[T](opts)
}

func ForType(t reflect.Type, opts *ForOptions) (*Schema, error) {
	return jsonschema.ForType(t, opts)
}
