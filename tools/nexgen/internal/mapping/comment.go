package mapping

import (
	"regexp"
	"strings"

	pgs "github.com/lyft/protoc-gen-star/v2"
)

// directive matches comment lines meant for other tools, such as
// temporal:versioning:min_version=v0.3.0 (DESIGN.md §5.5).
var directive = regexp.MustCompile(`^\s*temporal:[a-z_]+:`)

// Comment is the description for e: its leading comment, then its trailing
// comment as a new paragraph. Detached comments are ignored.
func Comment(e pgs.Entity) string {
	info := e.SourceCodeInfo()
	if info == nil {
		return ""
	}
	return paragraphs(cleanComment(info.LeadingComments()), cleanComment(info.TrailingComments()))
}

// cleanComment drops directive lines, strips the common indentation and
// trailing whitespace, and trims blank lines at either end. Markdown is kept.
func cleanComment(c string) string {
	var lines []string
	for _, l := range strings.Split(c, "\n") {
		if directive.MatchString(l) {
			continue
		}
		lines = append(lines, strings.TrimRight(l, " \t\r"))
	}
	indent := -1
	for _, l := range lines {
		if strings.TrimSpace(l) == "" {
			continue
		}
		n := len(l) - len(strings.TrimLeft(l, " \t"))
		if indent < 0 || n < indent {
			indent = n
		}
	}
	for i, l := range lines {
		if len(l) >= indent && indent > 0 {
			lines[i] = l[indent:]
		}
	}
	return strings.Trim(strings.Join(lines, "\n"), "\n")
}

// paragraphs joins the non-empty parts with blank lines.
func paragraphs(parts ...string) string {
	var out []string
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, "\n\n")
}

// oneLine collapses a comment to a single line for an enum value list.
func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
