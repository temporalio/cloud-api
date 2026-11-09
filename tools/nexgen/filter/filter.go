// Package filter loads nexgen.filter.yaml and matches proto elements against
// it (DESIGN.md §5.8).
//
// The plugin uses it to leave RPCs and fields out of the nexusrpc/
// definitions. It is public so the Nexus handler can clear the same fields
// from its responses with the same rules.
package filter

import (
	"bytes"
	"fmt"
	"os"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// Filter is a parsed filter file.
type Filter struct {
	// Path is the file name used in messages and generated headers.
	Path       string
	methods    []*rule
	fields     []*rule
	fieldTypes []*rule
}

type rule struct {
	section string
	index   int
	pattern string
	segs    []string
	matched bool
}

type file struct {
	Exclude struct {
		Methods    []string `yaml:"methods"`
		Fields     []string `yaml:"fields"`
		FieldTypes []string `yaml:"field_types"`
	} `yaml:"exclude"`
}

// Load reads and parses the filter file at path.
func Load(path string) (*Filter, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("filter: %w", err)
	}
	return Parse(path, data)
}

// Parse parses filter file content. path is used only in messages.
func Parse(path string, data []byte) (*Filter, error) {
	var f file
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&f); err != nil && err.Error() != "EOF" {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	out := &Filter{Path: path}
	var err error
	if out.methods, err = parseRules(path, "methods", f.Exclude.Methods); err != nil {
		return nil, err
	}
	if out.fields, err = parseRules(path, "fields", f.Exclude.Fields); err != nil {
		return nil, err
	}
	if out.fieldTypes, err = parseRules(path, "field_types", f.Exclude.FieldTypes); err != nil {
		return nil, err
	}
	return out, nil
}

var ident = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func parseRules(path, section string, patterns []string) ([]*rule, error) {
	rules := make([]*rule, 0, len(patterns))
	for i, p := range patterns {
		segs := strings.Split(p, ".")
		for _, s := range segs {
			if s != "*" && s != "**" && !ident.MatchString(s) {
				return nil, fmt.Errorf("%s: exclude.%s[%d]: %q: segment %q must be a proto identifier, * (one segment), or ** (any number of segments)", path, section, i, p, s)
			}
		}
		rules = append(rules, &rule{section: section, index: i, pattern: p, segs: segs})
	}
	return rules, nil
}

// ExcludesMethod reports whether the RPC with full name
// <service fqn>.<Method> is excluded.
func (f *Filter) ExcludesMethod(fqn string) bool {
	return f != nil && matchAny(f.methods, fqn)
}

// ExcludesField reports whether the field with full name
// <package>.<Message>[.<Nested>...].<field> is excluded, either by name or
// because its type is excluded. typeFQN is the full name of the field's
// message or enum type (the value type of a repeated or map field), or "" for
// a scalar.
func (f *Filter) ExcludesField(fieldFQN, typeFQN string) bool {
	if f == nil {
		return false
	}
	// Evaluate both so every matching rule is marked as used.
	byName := matchAny(f.fields, fieldFQN)
	byType := typeFQN != "" && matchAny(f.fieldTypes, typeFQN)
	return byName || byType
}

// Unmatched returns an error for every rule that has not matched anything,
// which catches typos and rules left behind after a proto rename.
func (f *Filter) Unmatched() []error {
	if f == nil {
		return nil
	}
	var errs []error
	for _, rules := range [][]*rule{f.methods, f.fields, f.fieldTypes} {
		for _, r := range rules {
			if !r.matched {
				errs = append(errs, fmt.Errorf("%s: exclude.%s[%d]: %q matched nothing", f.Path, r.section, r.index, r.pattern))
			}
		}
	}
	return errs
}

func matchAny(rules []*rule, name string) bool {
	name = strings.TrimPrefix(name, ".")
	segs := strings.Split(name, ".")
	hit := false
	for _, r := range rules {
		if match(r.segs, segs) {
			r.matched = true
			hit = true
		}
	}
	return hit
}

// match compares dot-separated segments: * matches exactly one segment and
// ** matches zero or more.
func match(pattern, name []string) bool {
	if len(pattern) == 0 {
		return len(name) == 0
	}
	switch pattern[0] {
	case "**":
		for i := 0; i <= len(name); i++ {
			if match(pattern[1:], name[i:]) {
				return true
			}
		}
		return false
	case "*":
		return len(name) > 0 && match(pattern[1:], name[1:])
	}
	return len(name) > 0 && pattern[0] == name[0] && match(pattern[1:], name[1:])
}
