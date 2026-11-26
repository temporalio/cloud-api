package closure

import (
	"reflect"
	"sort"
	"testing"
)

func TestWalk(t *testing.T) {
	graph := map[string][]string{
		"Req":    {"A", "B"},
		"Resp":   {"A"},
		"A":      {"C"},
		"B":      {"B"}, // self-reference
		"C":      {"A"}, // A -> C -> A
		"Unused": {"A"},
		"Dangle": {"Ghost"},
	}
	refs := func(fqn string) ([]string, bool) {
		out, ok := graph[fqn]
		return out, ok
	}

	res := Walk([]string{"Req", "Resp"}, refs)

	var marked []string
	for k := range res.Marked {
		marked = append(marked, k)
	}
	sort.Strings(marked)
	if want := []string{"A", "B", "C", "Req", "Resp"}; !reflect.DeepEqual(marked, want) {
		t.Errorf("Marked = %v, want %v", marked, want)
	}
	if want := []string{"A -> C -> A", "B -> B"}; !reflect.DeepEqual(res.Cycles, want) {
		t.Errorf("Cycles = %v, want %v", res.Cycles, want)
	}
	if len(res.Missing) != 0 {
		t.Errorf("Missing = %v, want none", res.Missing)
	}
}

func TestWalkMissing(t *testing.T) {
	graph := map[string][]string{"Req": {"Ghost"}, "Resp": {"Ghost"}}
	refs := func(fqn string) ([]string, bool) {
		out, ok := graph[fqn]
		return out, ok
	}
	res := Walk([]string{"Req", "Resp"}, refs)
	if want := map[string][]string{"Ghost": {"Req", "Resp"}}; !reflect.DeepEqual(res.Missing, want) {
		t.Errorf("Missing = %v, want %v", res.Missing, want)
	}
	if res.Marked["Ghost"] {
		t.Error("a missing def must not be marked")
	}
}
