// Package schema holds the JSON Schema nodes the mapping builds, before they
// are placed in files.
//
// A reference to another def is kept symbolic (the target's proto full name)
// until assembly, so the closure can follow refs without knowing file layout.
package schema

import (
	"sort"

	"github.com/temporalio/cloud-api/tools/nexgen/internal/emit"
)

// Node is one JSON Schema node. An object with no properties and no
// additionalProperties is written with an explicit empty properties map,
// because nexgen requires every object to declare its shape.
//
// Doc writes the keys in a fixed order:
// description, deprecated, type/$ref, type-specific keywords, then x-*
// extensions sorted by name (DESIGN.md §3, Determinism).
type Node struct {
	Description string
	Deprecated  bool

	Type string
	Ref  string // proto full name of the referenced def

	Format          string
	Pattern         string
	ContentEncoding string
	Minimum         *int64
	Maximum         *int64
	Items           *Node
	Properties      []Property
	// AdditionalProperties is the map value schema.
	AdditionalProperties *Node
	// OpenMap emits additionalProperties: true (google.protobuf.Struct).
	OpenMap       bool
	PropertyNames *Node
	Required      []string

	Extensions []Extension
}

// Property is a named entry of Node.Properties.
type Property struct {
	Name   string
	Schema *Node
}

// Extension is an x-* keyword with a string value.
type Extension struct {
	Key   string
	Value string
}

// Int returns a pointer to v, for Minimum and Maximum.
func Int(v int64) *int64 { return &v }

// Refs calls fn with every Ref in n and its children, in document order.
func (n *Node) Refs(fn func(fqn string)) {
	if n == nil {
		return
	}
	if n.Ref != "" {
		fn(n.Ref)
	}
	n.Items.Refs(fn)
	for _, p := range n.Properties {
		p.Schema.Refs(fn)
	}
	n.AdditionalProperties.Refs(fn)
	n.PropertyNames.Refs(fn)
}

// Doc converts n to an emit tree. resolve turns a proto full name into the
// $ref string for the file being written.
func (n *Node) Doc(resolve func(fqn string) string) *emit.Map {
	m := &emit.Map{}
	if n.Description != "" {
		m.Set("description", n.Description)
	}
	if n.Deprecated {
		m.Set("deprecated", true)
	}
	if n.Type != "" {
		m.Set("type", n.Type)
	}
	if n.Ref != "" {
		m.Set("$ref", emit.Quoted(resolve(n.Ref)))
	}
	if n.Format != "" {
		m.Set("format", n.Format)
	}
	if n.Pattern != "" {
		m.Set("pattern", n.Pattern)
	}
	if n.ContentEncoding != "" {
		m.Set("contentEncoding", n.ContentEncoding)
	}
	if n.Minimum != nil {
		m.Set("minimum", *n.Minimum)
	}
	if n.Maximum != nil {
		m.Set("maximum", *n.Maximum)
	}
	if n.Items != nil {
		m.Set("items", n.Items.Doc(resolve))
	}
	if len(n.Properties) > 0 || (n.Type == "object" && n.AdditionalProperties == nil && !n.OpenMap) {
		props := &emit.Map{}
		for _, p := range n.Properties {
			props.Set(p.Name, p.Schema.Doc(resolve))
		}
		m.Set("properties", props)
	}
	if n.AdditionalProperties != nil {
		m.Set("additionalProperties", n.AdditionalProperties.Doc(resolve))
	} else if n.OpenMap {
		m.Set("additionalProperties", true)
	}
	if n.PropertyNames != nil {
		m.Set("propertyNames", n.PropertyNames.Doc(resolve))
	}
	if len(n.Required) > 0 {
		l := &emit.List{Flow: true}
		for _, r := range n.Required {
			l.Items = append(l.Items, emit.Quoted(r))
		}
		m.Set("required", l)
	}
	exts := append([]Extension(nil), n.Extensions...)
	sort.SliceStable(exts, func(i, j int) bool { return exts[i].Key < exts[j].Key })
	for _, e := range exts {
		m.Set(e.Key, e.Value)
	}
	return m
}
