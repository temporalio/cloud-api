// Package mapping turns the PG* AST into schema nodes, one def per message,
// keyed by proto full name (DESIGN.md §5).
//
// Every message is mapped, used or not. A construct the mapping can't handle
// is recorded as a Diag on the field instead of failing. The caller reports
// it only if the field survives the filter and the closure reaches its
// message (DESIGN.md §3).
package mapping

import (
	"fmt"
	"math"
	"sort"
	"strings"

	pgs "github.com/lyft/protoc-gen-star/v2"

	"github.com/temporalio/cloud-api/tools/nexgen/filter"
	"github.com/temporalio/cloud-api/tools/nexgen/internal/naming"
	"github.com/temporalio/cloud-api/tools/nexgen/internal/schema"
)

// Diag is a diagnostic tied to a source location.
type Diag struct {
	File    string
	Line    int
	Subject string // <Message>.<field>, or <Service>.<Method>
	Msg     string
}

func (d Diag) String() string {
	return fmt.Sprintf("%s:%d: %s: %s", d.File, d.Line, d.Subject, d.Msg)
}

// Def is a message and its schema.
type Def struct {
	FQN     string
	Package string
	File    string // proto file path
	// Dependency is set when the file isn't one the plugin was asked to
	// generate, such as temporal.api.common.v1 from temporalio/api.
	Dependency bool
	// At locates the declaration, for diagnostics about the def itself.
	At     Diag
	Fields []*Field
	// Node is built by Finalize.
	Node *schema.Node

	description string
	deprecated  bool
}

// Field is one field of a message, before filtering.
type Field struct {
	FQN     string // <package>.<Message>.<field>
	JSON    string
	Number  int32
	TypeFQN string // message or enum type of the value, "" for scalars
	OneOf   string // real oneof name, "" otherwise
	Node    *schema.Node
	Err     *Diag
	At      Diag
}

// Registry holds every mapped def.
type Registry struct {
	Defs map[string]*Def
	// Order lists the defs by package, then source file path, then a
	// pre-order walk of declarations (DESIGN.md §3, Determinism).
	Order []*Def
}

// Refs returns the outgoing references of the def named fqn, in document
// order, and false if there is no such def. Call it after Finalize.
func (r *Registry) Refs(fqn string) ([]string, bool) {
	d, ok := r.Defs[fqn]
	if !ok {
		return nil, false
	}
	var out []string
	d.Node.Refs(func(ref string) { out = append(out, ref) })
	return out, true
}

// wktPackage holds the well-known types. It is never emitted (DESIGN.md §4).
const wktPackage = "google.protobuf"

// Build maps every message in pkgs.
func Build(pkgs map[string]pgs.Package) *Registry {
	r := &Registry{Defs: map[string]*Def{}}
	names := make([]string, 0, len(pkgs))
	for name := range pkgs {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if name == wktPackage {
			continue
		}
		files := append([]pgs.File(nil), pkgs[name].Files()...)
		sort.Slice(files, func(i, j int) bool { return files[i].Name() < files[j].Name() })
		for _, f := range files {
			r.addMessages(f.Messages())
		}
	}
	return r
}

// addMessages adds messages in declaration order, each followed directly by
// its nested messages.
func (r *Registry) addMessages(msgs []pgs.Message) {
	type decl struct {
		pos [2]int
		msg pgs.Message
	}
	var decls []decl
	for i, m := range msgs {
		if !m.IsMapEntry() {
			decls = append(decls, decl{pos: position(m, i), msg: m})
		}
	}
	sort.SliceStable(decls, func(i, j int) bool {
		a, b := decls[i].pos, decls[j].pos
		return a[0] < b[0] || (a[0] == b[0] && a[1] < b[1])
	})
	for _, d := range decls {
		def := mapMessage(d.msg)
		r.Defs[def.FQN] = def
		r.Order = append(r.Order, def)
		r.addMessages(d.msg.Messages())
	}
}

// position orders declarations by source span. Without source info, it
// falls back to index order after anything that has a span.
func position(e pgs.Entity, index int) [2]int {
	if info := e.SourceCodeInfo(); info != nil {
		if span := info.Location().GetSpan(); len(span) >= 2 {
			return [2]int{int(span[0]), int(span[1])}
		}
	}
	return [2]int{math.MaxInt32, index}
}

// Here is the diagnostic location of e.
func Here(e pgs.Entity, subject, format string, args ...any) Diag {
	line := 0
	if info := e.SourceCodeInfo(); info != nil {
		if span := info.Location().GetSpan(); len(span) > 0 {
			line = int(span[0]) + 1
		}
	}
	return Diag{File: e.File().Name().String(), Line: line, Subject: subject, Msg: fmt.Sprintf(format, args...)}
}

// relName is the full name of e without its package.
func relName(e pgs.Entity) string {
	return strings.TrimPrefix(e.FullyQualifiedName(), "."+e.Package().ProtoName().String()+".")
}

func fqn(e pgs.Entity) string { return strings.TrimPrefix(e.FullyQualifiedName(), ".") }

func mapMessage(m pgs.Message) *Def {
	d := &Def{
		FQN:         fqn(m),
		Package:     m.Package().ProtoName().String(),
		File:        m.File().Name().String(),
		Dependency:  !m.BuildTarget(),
		At:          Here(m, relName(m), ""),
		description: Comment(m),
		deprecated:  m.Descriptor().GetOptions().GetDeprecated(),
	}
	for _, f := range m.Fields() {
		subject := relName(m) + "." + f.Name().String()
		fld := &Field{
			FQN:    fqn(f),
			JSON:   JSONName(f),
			Number: f.Descriptor().GetNumber(),
			At:     Here(f, subject, ""),
		}
		if f.InRealOneOf() {
			fld.OneOf = f.OneOf().Name().String()
		}
		fld.TypeFQN = valueTypeFQN(f.Type())
		n, err := mapField(f)
		if err != nil {
			e := Here(f, subject, "%v", err)
			fld.Err = &e
		} else {
			fld.Node = n
		}
		d.Fields = append(d.Fields, fld)
	}
	sort.SliceStable(d.Fields, func(i, j int) bool { return d.Fields[i].Number < d.Fields[j].Number })
	return d
}

// valueTypeFQN is the message or enum type of a field's values, for
// field_types filter rules.
func valueTypeFQN(t pgs.FieldType) string {
	var v valueType = t
	if t.IsMap() || t.IsRepeated() {
		v = t.Element()
	}
	switch {
	case v.IsEmbed():
		return fqn(v.Embed())
	case v.IsEnum():
		return fqn(v.Enum())
	}
	return ""
}

// Finalize builds d.Node from the fields the filter keeps and returns the
// diagnostics of those fields. oneof notes name only the remaining members
// (DESIGN.md §5.8).
func (d *Def) Finalize(f *filter.Filter) []Diag {
	var kept []*Field
	for _, fld := range d.Fields {
		if !f.ExcludesField(fld.FQN, fld.TypeFQN) {
			kept = append(kept, fld)
		}
	}
	members := map[string][]string{}
	for _, fld := range kept {
		if fld.OneOf != "" {
			members[fld.OneOf] = append(members[fld.OneOf], fld.JSON)
		}
	}
	d.Node = &schema.Node{Type: "object", Description: d.description, Deprecated: d.deprecated}
	var diags []Diag
	for _, fld := range kept {
		if fld.Err != nil {
			diags = append(diags, *fld.Err)
			continue
		}
		n := *fld.Node
		if others := without(members[fld.OneOf], fld.JSON); len(others) > 0 {
			n.Description = paragraphs(n.Description, fmt.Sprintf("Mutually exclusive with %s (oneof `%s`).", ticked(others), fld.OneOf))
		}
		if n.Ref != "" {
			// nexgen forks a new type for a $ref with a description beside
			// it, so a reference keeps only deprecated (DESIGN.md Q2).
			n.Description = ""
		}
		d.Node.Properties = append(d.Node.Properties, schema.Property{Name: fld.JSON, Schema: &n})
	}
	return diags
}

func without(names []string, name string) []string {
	var out []string
	for _, n := range names {
		if n != name {
			out = append(out, n)
		}
	}
	return out
}

func ticked(names []string) string {
	q := make([]string, len(names))
	for i, n := range names {
		q[i] = "`" + n + "`"
	}
	return strings.Join(q, ", ")
}

// JSONName is the field's protojson key.
func JSONName(f pgs.Field) string {
	if n := f.Descriptor().GetJsonName(); n != "" {
		return n
	}
	// protoc always sets json_name for plugins; this mirrors its rule for
	// requests built by other tools.
	var b strings.Builder
	upper := false
	for _, c := range f.Name().String() {
		if c == '_' {
			upper = true
			continue
		}
		if upper && 'a' <= c && c <= 'z' {
			c -= 'a' - 'A'
		}
		upper = false
		b.WriteRune(c)
	}
	return b.String()
}

// valueType is the part of pgs.FieldType and pgs.FieldTypeElem that
// describes a single value.
type valueType interface {
	ProtoType() pgs.ProtoType
	IsEmbed() bool
	IsEnum() bool
	Embed() pgs.Message
	Enum() pgs.Enum
}

func mapField(f pgs.Field) (*schema.Node, error) {
	t := f.Type()
	var n *schema.Node
	var err error
	switch {
	case t.IsMap():
		n, err = mapMap(t)
	case t.IsRepeated():
		var items *schema.Node
		if items, err = mapValue(t.Element()); err == nil {
			n = &schema.Node{Type: "array", Items: items}
			// The enum value list describes the field, not each item.
			n.Description, items.Description = items.Description, ""
		}
	default:
		n, err = mapValue(t)
	}
	if err != nil {
		return nil, err
	}
	n.Description = paragraphs(Comment(f), n.Description)
	n.Deprecated = f.Descriptor().GetOptions().GetDeprecated() || strings.HasSuffix(f.Name().String(), "_deprecated")
	for _, o := range naming.KeywordOverrides(JSONName(f), true) {
		n.Extensions = append(n.Extensions, schema.Extension{Key: o.Key, Value: o.Name})
	}
	return n, nil
}

func mapMap(t pgs.FieldType) (*schema.Node, error) {
	v, err := mapValue(t.Element())
	if err != nil {
		return nil, err
	}
	n := &schema.Node{Type: "object", AdditionalProperties: v}
	n.Description, v.Description = v.Description, ""
	if kt := t.Key().ProtoType(); kt != pgs.StringT {
		p, err := mapKeyPattern(kt)
		if err != nil {
			return nil, err
		}
		n.PropertyNames = &schema.Node{Type: "string", Pattern: p}
	}
	return n, nil
}

func mapValue(t valueType) (*schema.Node, error) {
	switch {
	case t.IsEnum():
		return mapEnum(t.Enum())
	case t.IsEmbed():
		m := t.Embed()
		if m.Package().ProtoName().String() == wktPackage {
			return mapWKT(fqn(m))
		}
		return &schema.Node{Ref: fqn(m)}, nil
	}
	return mapScalar(t.ProtoType())
}

func mapScalar(pt pgs.ProtoType) (*schema.Node, error) {
	switch pt {
	case pgs.StringT:
		return &schema.Node{Type: "string"}, nil
	case pgs.BoolT:
		return &schema.Node{Type: "boolean"}, nil
	case pgs.Int32T, pgs.SInt32, pgs.SFixed32:
		return &schema.Node{Type: "integer", Minimum: schema.Int(math.MinInt32), Maximum: schema.Int(math.MaxInt32)}, nil
	case pgs.UInt32T, pgs.Fixed32T:
		return &schema.Node{Type: "integer", Minimum: schema.Int(0), Maximum: schema.Int(math.MaxUint32)}, nil
	case pgs.Int64T, pgs.SInt64, pgs.SFixed64:
		return &schema.Node{Type: "string", Pattern: "^-?[0-9]+$"}, nil
	case pgs.UInt64T, pgs.Fixed64T:
		return &schema.Node{Type: "string", Pattern: "^[0-9]+$"}, nil
	case pgs.FloatT, pgs.DoubleT:
		return &schema.Node{Type: "number"}, nil
	case pgs.BytesT:
		return &schema.Node{Type: "string", ContentEncoding: "base64"}, nil
	case pgs.GroupT:
		return nil, fmt.Errorf("proto2 groups are not supported; use a message field")
	}
	return nil, fmt.Errorf("unsupported field type %s", pt)
}

// mapWKT inlines a well-known type as its protojson form (DESIGN.md §5.3).
func mapWKT(name string) (*schema.Node, error) {
	switch strings.TrimPrefix(name, wktPackage+".") {
	case "Timestamp":
		return &schema.Node{Type: "string", Format: "date-time"}, nil
	case "Duration":
		return &schema.Node{Type: "string", Pattern: `^-?[0-9]+(\.[0-9]{1,9})?s$`}, nil
	case "Struct":
		return &schema.Node{Type: "object", OpenMap: true}, nil
	case "Any":
		return &schema.Node{
			Type:       "object",
			Properties: []schema.Property{{Name: "@type", Schema: &schema.Node{Type: "string"}}},
			Required:   []string{"@type"},
		}, nil
	case "Empty":
		return &schema.Node{Type: "object"}, nil
	case "FieldMask":
		return &schema.Node{Type: "string", Description: "Comma-separated lowerCamelCase field paths."}, nil
	case "DoubleValue", "FloatValue":
		return mapScalar(pgs.DoubleT)
	case "Int64Value":
		return mapScalar(pgs.Int64T)
	case "UInt64Value":
		return mapScalar(pgs.UInt64T)
	case "Int32Value":
		return mapScalar(pgs.Int32T)
	case "UInt32Value":
		return mapScalar(pgs.UInt32T)
	case "BoolValue":
		return mapScalar(pgs.BoolT)
	case "StringValue":
		return mapScalar(pgs.StringT)
	case "BytesValue":
		return mapScalar(pgs.BytesT)
	case "Value", "ListValue", "NullValue":
		return nil, fmt.Errorf("%s can't be expressed: nexgen has no \"any JSON value\" type; use google.protobuf.Struct or a message", name)
	}
	return nil, fmt.Errorf("%s is not a supported well-known type", name)
}

// mapKeyPattern is the propertyNames pattern for a non-string map key's
// protojson form.
func mapKeyPattern(pt pgs.ProtoType) (string, error) {
	switch pt {
	case pgs.Int32T, pgs.SInt32, pgs.SFixed32, pgs.Int64T, pgs.SInt64, pgs.SFixed64:
		return "^-?[0-9]+$", nil
	case pgs.UInt32T, pgs.Fixed32T, pgs.UInt64T, pgs.Fixed64T:
		return "^[0-9]+$", nil
	case pgs.BoolT:
		return "^(true|false)$", nil
	}
	return "", fmt.Errorf("unsupported map key type %s", pt)
}

// mapEnum inlines an enum as an open string (D13). nexgen only allows object
// types in $defs, has no open enum, and rejects unknown x-* keywords, so the
// known names are listed in the description (DESIGN.md §5.4).
func mapEnum(e pgs.Enum) (*schema.Node, error) {
	if e.Package().ProtoName().String() == wktPackage {
		return mapWKT(fqn(e))
	}
	var lines []string
	seen := map[int32]bool{}
	for _, v := range e.Values() {
		// An alias is listed once, under the first name for its number;
		// protojson always writes that name.
		num := v.Descriptor().GetNumber()
		if seen[num] {
			continue
		}
		seen[num] = true
		line := "- `" + v.Name().String() + "`"
		if c := oneLine(Comment(v)); c != "" {
			line += ": " + c
		}
		if v.Descriptor().GetOptions().GetDeprecated() {
			line += " (deprecated)"
		}
		lines = append(lines, line)
	}
	head := fmt.Sprintf("One of the `%s` values:", relName(e))
	if e.Descriptor().GetOptions().GetDeprecated() {
		head = fmt.Sprintf("One of the `%s` values (the enum is deprecated):", relName(e))
	}
	desc := head + "\n\n" + strings.Join(lines, "\n") + "\n\nNewer servers may return values not listed here."
	return &schema.Node{Type: "string", Description: desc}, nil
}
