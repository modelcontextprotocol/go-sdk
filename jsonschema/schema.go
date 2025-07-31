// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package jsonschema

import (
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"maps"
	"math"
	"reflect"
	"slices"

	"github.com/modelcontextprotocol/go-sdk/internal/util"
)

// A Schema is a JSON schema object.
// It supports both draft-07 and the 2020-12 draft specifications:
//   - Draft-07: http://json-schema.org/draft-07/schema#
//   - Draft 2020-12: https://json-schema.org/draft/2020-12/draft-bhutton-json-schema-01
//     and https://json-schema.org/draft/2020-12/draft-bhutton-json-schema-validation-01
//
// A Schema value may have non-zero values for more than one field:
// all relevant non-zero fields are used for validation.
// There is one exception to provide more Go type-safety: the Type and Types fields
// are mutually exclusive.
//
// Since this struct is a Go representation of a JSON value, it inherits JSON's
// distinction between nil and empty. Nil slices and maps are considered absent,
// but empty ones are present and affect validation. For example,
//
//	Schema{Enum: nil}
//
// is equivalent to an empty schema, so it validates every instance. But
//
//	Schema{Enum: []any{}}
//
// requires equality to some slice element, so it vacuously rejects every instance.
type Schema struct {
	// core
	ID      string             `json:"$id,omitempty"`
	Schema  string             `json:"$schema,omitempty"`
	Ref     string             `json:"$ref,omitempty"`
	Comment string             `json:"$comment,omitempty"`
	Defs    map[string]*Schema `json:"$defs,omitempty"`
	// definitions is deprecated but still allowed. It is a synonym for $defs.
	Definitions map[string]*Schema `json:"definitions,omitempty"`

	Anchor        string          `json:"$anchor,omitempty"`
	DynamicAnchor string          `json:"$dynamicAnchor,omitempty"`
	DynamicRef    string          `json:"$dynamicRef,omitempty"`
	Vocabulary    map[string]bool `json:"$vocabulary,omitempty"`

	// metadata
	Title       string          `json:"title,omitempty"`
	Description string          `json:"description,omitempty"`
	Default     json.RawMessage `json:"default,omitempty"`
	Deprecated  bool            `json:"deprecated,omitempty"`
	ReadOnly    bool            `json:"readOnly,omitempty"`
	WriteOnly   bool            `json:"writeOnly,omitempty"`
	Examples    []any           `json:"examples,omitempty"`

	// validation
	// Use Type for a single type, or Types for multiple types; never both.
	Type  string   `json:"-"`
	Types []string `json:"-"`
	Enum  []any    `json:"enum,omitempty"`
	// Const is *any because a JSON null (Go nil) is a valid value.
	Const            *any     `json:"const,omitempty"`
	MultipleOf       *float64 `json:"multipleOf,omitempty"`
	Minimum          *float64 `json:"minimum,omitempty"`
	Maximum          *float64 `json:"maximum,omitempty"`
	ExclusiveMinimum *float64 `json:"exclusiveMinimum,omitempty"`
	ExclusiveMaximum *float64 `json:"exclusiveMaximum,omitempty"`
	MinLength        *int     `json:"minLength,omitempty"`
	MaxLength        *int     `json:"maxLength,omitempty"`
	Pattern          string   `json:"pattern,omitempty"`

	// arrays
	PrefixItems      []*Schema `json:"prefixItems,omitempty"`
	Items            *Schema   `json:"items,omitempty"`
	MinItems         *int      `json:"minItems,omitempty"`
	MaxItems         *int      `json:"maxItems,omitempty"`
	AdditionalItems  *Schema   `json:"additionalItems,omitempty"`
	UniqueItems      bool      `json:"uniqueItems,omitempty"`
	Contains         *Schema   `json:"contains,omitempty"`
	MinContains      *int      `json:"minContains,omitempty"` // *int, not int: default is 1, not 0
	MaxContains      *int      `json:"maxContains,omitempty"`
	UnevaluatedItems *Schema   `json:"unevaluatedItems,omitempty"`

	// objects
	MinProperties         *int                `json:"minProperties,omitempty"`
	MaxProperties         *int                `json:"maxProperties,omitempty"`
	Required              []string            `json:"required,omitempty"`
	DependentRequired     map[string][]string `json:"dependentRequired,omitempty"`
	Properties            map[string]*Schema  `json:"properties,omitempty"`
	PatternProperties     map[string]*Schema  `json:"patternProperties,omitempty"`
	AdditionalProperties  *Schema             `json:"additionalProperties,omitempty"`
	PropertyNames         *Schema             `json:"propertyNames,omitempty"`
	UnevaluatedProperties *Schema             `json:"unevaluatedProperties,omitempty"`

	// draft-07 specific - Dependencies field that was split in draft 2020-12
	Dependencies map[string]any `json:"dependencies,omitempty"`

	// logic
	AllOf []*Schema `json:"allOf,omitempty"`
	AnyOf []*Schema `json:"anyOf,omitempty"`
	OneOf []*Schema `json:"oneOf,omitempty"`
	Not   *Schema   `json:"not,omitempty"`

	// conditional
	If               *Schema            `json:"if,omitempty"`
	Then             *Schema            `json:"then,omitempty"`
	Else             *Schema            `json:"else,omitempty"`
	DependentSchemas map[string]*Schema `json:"dependentSchemas,omitempty"`

	// other
	// https://json-schema.org/draft/2020-12/draft-bhutton-json-schema-validation-00#rfc.section.8
	ContentEncoding  string  `json:"contentEncoding,omitempty"`
	ContentMediaType string  `json:"contentMediaType,omitempty"`
	ContentSchema    *Schema `json:"contentSchema,omitempty"`

	// https://json-schema.org/draft/2020-12/draft-bhutton-json-schema-validation-00#rfc.section.7
	Format string `json:"format,omitempty"`

	// Extra allows for additional keywords beyond those specified.
	Extra map[string]any `json:"-"`
}

// falseSchema returns a new Schema tree that fails to validate any value.
func falseSchema() *Schema {
	return &Schema{Not: &Schema{}}
}

// anchorInfo records the subschema to which an anchor refers, and whether
// the anchor keyword is $anchor or $dynamicAnchor.
type anchorInfo struct {
	schema  *Schema
	dynamic bool
}

// String returns a short description of the schema.
func (s *Schema) String() string {
	if s.ID != "" {
		return s.ID
	}
	if a := cmp.Or(s.Anchor, s.DynamicAnchor); a != "" {
		return fmt.Sprintf("anchor %s", a)
	}
	return "<anonymous schema>"
}

func (s *Schema) basicChecks() error {
	if s.Type != "" && s.Types != nil {
		return errors.New("both Type and Types are set; at most one should be")
	}
	if s.Defs != nil && s.Definitions != nil {
		return errors.New("both Defs and Definitions are set; at most one should be")
	}
	return nil
}

type schemaWithoutMethods Schema // doesn't implement json.{Unm,M}arshaler

func (s *Schema) MarshalJSON() ([]byte, error) {
	if err := s.basicChecks(); err != nil {
		return nil, err
	}
	// Marshal either Type or Types as "type".
	var typ any
	switch {
	case s.Type != "":
		typ = s.Type
	case s.Types != nil:
		typ = s.Types
	}

	// For draft-07 compatibility: if we only have PrefixItems and no Items,
	// marshal PrefixItems as "items" array, otherwise marshal Items as single schema
	var items any
	if len(s.PrefixItems) > 0 && s.Items == nil {
		// This looks like a draft-07 items array converted to prefixItems
		items = s.PrefixItems
	} else if s.Items != nil {
		items = s.Items
	}

	ms := struct {
		Type  any `json:"type,omitempty"`
		Items any `json:"items,omitempty"`
		*schemaWithoutMethods
	}{
		Type:                 typ,
		Items:                items,
		schemaWithoutMethods: (*schemaWithoutMethods)(s),
	}
	return marshalStructWithMap(&ms, "Extra")
}

func (s *Schema) UnmarshalJSON(data []byte) error {
	// A JSON boolean is a valid schema.
	var b bool
	if err := json.Unmarshal(data, &b); err == nil {
		if b {
			// true is the empty schema, which validates everything.
			*s = Schema{}
		} else {
			// false is the schema that validates nothing.
			*s = *falseSchema()
		}
		return nil
	}

	ms := struct {
		Type          json.RawMessage `json:"type,omitempty"`
		Items         json.RawMessage `json:"items,omitempty"`
		Const         json.RawMessage `json:"const,omitempty"`
		MinLength     *integer        `json:"minLength,omitempty"`
		MaxLength     *integer        `json:"maxLength,omitempty"`
		MinItems      *integer        `json:"minItems,omitempty"`
		MaxItems      *integer        `json:"maxItems,omitempty"`
		MinProperties *integer        `json:"minProperties,omitempty"`
		MaxProperties *integer        `json:"maxProperties,omitempty"`
		MinContains   *integer        `json:"minContains,omitempty"`
		MaxContains   *integer        `json:"maxContains,omitempty"`

		*schemaWithoutMethods
	}{
		schemaWithoutMethods: (*schemaWithoutMethods)(s),
	}
	if err := unmarshalStructWithMap(data, &ms, "Extra"); err != nil {
		return err
	}
	// Unmarshal "type" as either Type or Types.
	var err error
	if len(ms.Type) > 0 {
		switch ms.Type[0] {
		case '"':
			err = json.Unmarshal(ms.Type, &s.Type)
		case '[':
			err = json.Unmarshal(ms.Type, &s.Types)
		default:
			err = fmt.Errorf(`invalid value for "type": %q`, ms.Type)
		}
	}
	if err != nil {
		return err
	}

	unmarshalAnyPtr := func(p **any, raw json.RawMessage) error {
		if len(raw) == 0 {
			return nil
		}
		if bytes.Equal(raw, []byte("null")) {
			*p = new(any)
			return nil
		}
		return json.Unmarshal(raw, p)
	}

	// Setting Const to a pointer to null will marshal properly, but won't
	// unmarshal: the *any is set to nil, not a pointer to nil.
	if err := unmarshalAnyPtr(&s.Const, ms.Const); err != nil {
		return err
	}

	set := func(dst **int, src *integer) {
		if src != nil {
			*dst = Ptr(int(*src))
		}
	}

	set(&s.MinLength, ms.MinLength)
	set(&s.MaxLength, ms.MaxLength)
	set(&s.MinItems, ms.MinItems)
	set(&s.MaxItems, ms.MaxItems)
	set(&s.MinProperties, ms.MinProperties)
	set(&s.MaxProperties, ms.MaxProperties)
	set(&s.MinContains, ms.MinContains)
	set(&s.MaxContains, ms.MaxContains)

	// Handle "items" field: can be either a schema or an array of schemas (draft-07)
	if len(ms.Items) > 0 {
		switch ms.Items[0] {
		case '{':
			// Single schema object
			err = json.Unmarshal(ms.Items, &s.Items)
		case '[':
			// Array of schemas (draft-07 tuple validation)
			// For draft-07, convert items array to prefixItems for compatibility
			err = json.Unmarshal(ms.Items, &s.PrefixItems)
		case 't', 'f':
			// Boolean schema
			var boolSchema bool
			if err = json.Unmarshal(ms.Items, &boolSchema); err == nil {
				if boolSchema {
					s.Items = &Schema{}
				} else {
					s.Items = falseSchema()
				}
			}
		default:
			err = fmt.Errorf(`invalid value for "items": %q`, ms.Items)
		}
		if err != nil {
			return err
		}
	}

	return nil
}

type integer int32 // for the integer-valued fields of Schema

func (ip *integer) UnmarshalJSON(data []byte) error {
	if len(data) == 0 {
		// nothing to do
		return nil
	}
	// If there is a decimal point, src is a floating-point number.
	var i int64
	if bytes.ContainsRune(data, '.') {
		var f float64
		if err := json.Unmarshal(data, &f); err != nil {
			return errors.New("not a number")
		}
		i = int64(f)
		if float64(i) != f {
			return errors.New("not an integer value")
		}
	} else {
		if err := json.Unmarshal(data, &i); err != nil {
			return errors.New("cannot be unmarshaled into an int")
		}
	}
	// Ensure behavior is the same on both 32-bit and 64-bit systems.
	if i < math.MinInt32 || i > math.MaxInt32 {
		return errors.New("integer is out of range")
	}
	*ip = integer(i)
	return nil
}

// Ptr returns a pointer to a new variable whose value is x.
func Ptr[T any](x T) *T { return &x }

// every applies f preorder to every schema under s including s.
// The second argument to f is the path to the schema appended to the argument path.
// It stops when f returns false.
func (s *Schema) every(f func(*Schema) bool) bool {
	return f(s) && s.everyChild(func(s *Schema) bool { return s.every(f) })
}

// everyChild reports whether f is true for every immediate child schema of s.
func (s *Schema) everyChild(f func(*Schema) bool) bool {
	v := reflect.ValueOf(s)
	for _, info := range schemaFieldInfos {
		fv := v.Elem().FieldByIndex(info.sf.Index)
		switch info.sf.Type {
		case schemaType:
			// A field that contains an individual schema. A nil is valid: it just means the field isn't present.
			c := fv.Interface().(*Schema)
			if c != nil && !f(c) {
				return false
			}

		case schemaSliceType:
			slice := fv.Interface().([]*Schema)
			for _, c := range slice {
				if !f(c) {
					return false
				}
			}

		case schemaMapType:
			// Sort keys for determinism.
			m := fv.Interface().(map[string]*Schema)
			for _, k := range slices.Sorted(maps.Keys(m)) {
				if !f(m[k]) {
					return false
				}
			}
		}
	}

	return true
}

// all wraps every in an iterator.
func (s *Schema) all() iter.Seq[*Schema] {
	return func(yield func(*Schema) bool) { s.every(yield) }
}

// children wraps everyChild in an iterator.
func (s *Schema) children() iter.Seq[*Schema] {
	return func(yield func(*Schema) bool) { s.everyChild(yield) }
}

var (
	schemaType      = reflect.TypeFor[*Schema]()
	schemaSliceType = reflect.TypeFor[[]*Schema]()
	schemaMapType   = reflect.TypeFor[map[string]*Schema]()
)

type structFieldInfo struct {
	sf       reflect.StructField
	jsonName string
}

var (
	// the visible fields of Schema that have a JSON name, sorted by that name
	schemaFieldInfos []structFieldInfo
	// map from JSON name to field
	schemaFieldMap = map[string]reflect.StructField{}
)

func init() {
	for _, sf := range reflect.VisibleFields(reflect.TypeFor[Schema]()) {
		info := util.FieldJSONInfo(sf)
		if !info.Omit {
			schemaFieldInfos = append(schemaFieldInfos, structFieldInfo{sf, info.Name})
		}
	}
	slices.SortFunc(schemaFieldInfos, func(i1, i2 structFieldInfo) int {
		return cmp.Compare(i1.jsonName, i2.jsonName)
	})
	for _, info := range schemaFieldInfos {
		schemaFieldMap[info.jsonName] = info.sf
	}
}
