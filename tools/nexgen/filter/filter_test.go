package filter

import (
	"strings"
	"testing"
)

func TestMatch(t *testing.T) {
	for _, tc := range []struct {
		pattern, name string
		want          bool
	}{
		{"a.b.C.f", "a.b.C.f", true},
		{"a.b.C.f", "a.b.C.g", false},
		{"a.*.C.f", "a.b.C.f", true},
		{"a.*.f", "a.b.C.f", false},
		{"a.**.f", "a.f", true},
		{"a.**.f", "a.b.C.D.f", true},
		{"a.**.f", "a.b.C.D.g", false},
		{"**", "anything.at.all", true},
		{"a.b", "a.b.c", false},
	} {
		f, err := Parse("f.yaml", []byte("exclude:\n  methods: [\""+tc.pattern+"\"]\n"))
		if err != nil {
			t.Fatal(err)
		}
		if got := f.ExcludesMethod("." + tc.name); got != tc.want {
			t.Errorf("%q against %q = %v, want %v", tc.pattern, tc.name, got, tc.want)
		}
	}
}

func TestExcludesField(t *testing.T) {
	f, err := Parse("f.yaml", []byte(`
exclude:
  fields: [pkg.**.async_operation_id]
  field_types: [pkg.op.AsyncOperation]
`))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		field, typ string
		want       bool
	}{
		{"pkg.v1.User.async_operation_id", "", true},
		{"pkg.v1.User.Spec.async_operation_id", "", true},
		{"pkg.v1.User.name", "", false},
		{"pkg.v1.Resp.async_operation", "pkg.op.AsyncOperation", true},
		{"pkg.v1.Resp.other", "pkg.op.Other", false},
	} {
		if got := f.ExcludesField(tc.field, tc.typ); got != tc.want {
			t.Errorf("ExcludesField(%q, %q) = %v, want %v", tc.field, tc.typ, got, tc.want)
		}
	}
	if errs := f.Unmatched(); len(errs) != 0 {
		t.Errorf("Unmatched() = %v, want none", errs)
	}
}

func TestUnmatched(t *testing.T) {
	f, err := Parse("f.yaml", []byte("exclude:\n  methods: [a.B.C]\n  fields: [x.y]\n"))
	if err != nil {
		t.Fatal(err)
	}
	f.ExcludesMethod("a.B.C")
	errs := f.Unmatched()
	if len(errs) != 1 || errs[0].Error() != `f.yaml: exclude.fields[0]: "x.y" matched nothing` {
		t.Errorf("Unmatched() = %v", errs)
	}
}

func TestParseErrors(t *testing.T) {
	for _, tc := range []struct{ yaml, want string }{
		{"exclude:\n  methods: [a.Get*]\n", `segment "Get*" must be a proto identifier`},
		{"exclude:\n  methods: [a..b]\n", `segment "" must be a proto identifier`},
		{"exclude:\n  method: [a.b]\n", "field method not found"},
	} {
		_, err := Parse("f.yaml", []byte(tc.yaml))
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("Parse(%q) error = %v, want it to contain %q", tc.yaml, err, tc.want)
		}
	}
}

func TestNilFilter(t *testing.T) {
	var f *Filter
	if f.ExcludesMethod("a.b") || f.ExcludesField("a.b", "c") || f.Unmatched() != nil {
		t.Error("a nil filter must exclude nothing")
	}
}
