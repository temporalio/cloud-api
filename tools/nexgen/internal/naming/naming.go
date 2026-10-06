// Package naming derives $defs keys, output file paths, operation keys, and
// keyword overrides from proto names (DESIGN.md §4, §6).
package naming

import (
	"fmt"
	"path"
	"regexp"
	"strings"
	"unicode"
)

// DefKey is the $defs key for a message or enum: its full name without the
// package, with the parent names of a nested type concatenated
// (CodecServerSpec.CustomErrorMessage → CodecServerSpecCustomErrorMessage).
func DefKey(pkg, fqn string) string {
	rel := strings.TrimPrefix(fqn, pkg+".")
	return strings.ReplaceAll(rel, ".", "")
}

var versionSegment = regexp.MustCompile(`^v[0-9]+((alpha|beta)[0-9]*)?$`)

// Segment is the last package segment before the version
// (temporal.api.cloud.namespace.v1 → namespace).
func Segment(pkg string) (string, error) {
	parts := strings.Split(pkg, ".")
	seg := parts[len(parts)-1]
	if versionSegment.MatchString(seg) {
		if len(parts) == 1 {
			return "", fmt.Errorf("package %q has no segment before its version", pkg)
		}
		seg = parts[len(parts)-2]
	}
	return seg, nil
}

// FilePath is the output path of a package's document, relative to the
// output directory: <pkg path>/<segment>.<ext>, where segment is the last
// package segment before the version. A package with a service gets a
// .nexusrpc.<ext> document.
func FilePath(pkg string, service bool, ext string) (string, error) {
	if pkg == "" {
		return "", fmt.Errorf("a package declaration is required")
	}
	seg, err := Segment(pkg)
	if err != nil {
		return "", err
	}
	parts := strings.Split(pkg, ".")
	name := seg + "." + ext
	if service {
		name = seg + ".nexusrpc." + ext
	}
	return path.Join(append(parts, name)...), nil
}

// snake converts a CamelCase name to snake_case, starting a new word at each
// lower→upper transition and before the last capital of an acronym
// (HTTPServer → HTTP_Server). Digits do not start a word.
func snake(s string) string {
	r := []rune(s)
	var b strings.Builder
	for i, c := range r {
		if i > 0 && unicode.IsUpper(c) && r[i-1] != '_' {
			prev := r[i-1]
			nextLower := i+1 < len(r) && unicode.IsLower(r[i+1])
			if unicode.IsLower(prev) || unicode.IsDigit(prev) || (unicode.IsUpper(prev) && nextLower) {
				b.WriteByte('_')
			}
		}
		b.WriteRune(c)
	}
	return b.String()
}
