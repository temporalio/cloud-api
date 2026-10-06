// Package closure computes which defs are reachable from a set of roots
// (DESIGN.md §3, step 3).
//
// The marks are returned as a separate set rather than stored on the nodes,
// so nothing about the walk can leak into the output.
package closure

import "strings"

// Result is the outcome of a closure walk.
type Result struct {
	// Marked holds every def reachable from the roots, including the roots.
	Marked map[string]bool
	// Missing maps a referenced name that refs could not resolve to the defs
	// that reference it, in walk order.
	Missing map[string][]string
	// Cycles lists each reference cycle found, as "A -> B -> A".
	Cycles []string
}

// Walk marks everything reachable from roots. refs returns the outgoing
// references of a def and false if the def doesn't exist.
func Walk(roots []string, refs func(fqn string) ([]string, bool)) Result {
	const (
		unvisited = iota
		active
		done
	)
	res := Result{Marked: map[string]bool{}, Missing: map[string][]string{}}
	state := map[string]int{}
	var stack []string

	var visit func(fqn, from string)
	visit = func(fqn, from string) {
		switch state[fqn] {
		case active:
			for i, s := range stack {
				if s == fqn {
					res.Cycles = append(res.Cycles, strings.Join(append(append([]string(nil), stack[i:]...), fqn), " -> "))
					break
				}
			}
			return
		case done:
			return
		}
		out, ok := refs(fqn)
		if !ok {
			state[fqn] = done
			res.Missing[fqn] = append(res.Missing[fqn], from)
			return
		}
		state[fqn] = active
		res.Marked[fqn] = true
		stack = append(stack, fqn)
		for _, r := range out {
			if state[r] == done && !res.Marked[r] {
				res.Missing[r] = append(res.Missing[r], fqn)
				continue
			}
			visit(r, fqn)
		}
		stack = stack[:len(stack)-1]
		state[fqn] = done
	}
	for _, r := range roots {
		visit(r, "")
	}
	return res
}
