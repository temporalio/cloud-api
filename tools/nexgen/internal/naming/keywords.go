package naming

import (
	"regexp"
	"sort"
	"strings"
	"unicode"
)

// Identifiers that nexgen rejects in each target language (DESIGN.md §6.3,
// Q7). The tables were measured against the pinned nexgen by generating a
// one-property model per candidate word. Go has none: nexgen exports Go
// identifiers, so a lower-case keyword never collides.
var keywords = map[string]map[string]bool{
	"java": set(`_ abstract assert boolean break byte case catch char class const continue
		default do double else enum extends false final finally float for goto if
		implements import instanceof int interface long native new null package private
		protected public return short static strictfp super switch synchronized this
		throw throws transient true try void volatile while`),
	"py": set(`and as assert async await break case class continue def del elif else
		except finally for from global if import in is lambda match nonlocal not or
		pass raise return try while with yield`),
	"ts": set(`as break case catch class const continue default delete do else enum
		export extends false finally for function if implements import in instanceof
		interface let new null package private protected public return static super
		switch this throw true try typeof var void while with yield`),
}

// javaDeserializerLocals are the locals of the deserializer nexgen generates
// for each Java model, which share a scope with the member slots. Copied from
// JAVA_DESERIALIZER_LOCALS in nexgen's src/parser/json_schema.rs, plus
// additionalProperties, which holds the catch-all.
var javaDeserializerLocals = set(`additionalProperties context element elementPath field
	fieldNames index items length nestedLength nestedViolations node numberValue parsed
	parser priorIndex rawElement rawIndex rawKey rawMatchCount rawSeen violation
	violations`)

// javaNestedLocal matches the loop locals nexgen mints for each level of a
// nested Java array: items1, index1, element1, path1, then 2, 3, ...
var javaNestedLocal = regexp.MustCompile(`^(items|index|element|path)[1-9][0-9]*$`)

func set(words string) map[string]bool {
	m := map[string]bool{}
	for _, w := range strings.Fields(words) {
		m[w] = true
	}
	return m
}

// KeywordOverride is an x-<lang>-name keyword for a name that is reserved in
// that language.
type KeywordOverride struct {
	Key  string // x-java-name, x-py-name, or x-ts-name
	Name string
}

// KeywordOverrides returns the overrides a property (member) or operation
// key needs, sorted by keyword. The identifier each language derives from key
// is compared, not the key itself: Python snake-cases it and Java and
// TypeScript lower-camel-case it.
func KeywordOverrides(key string, member bool) []KeywordOverride {
	var out []KeywordOverride
	for lang, words := range keywords {
		id := lowerFirst(key)
		if lang == "py" {
			id = strings.ToLower(snake(key))
		}
		reserved := words[id]
		if member && lang == "java" {
			reserved = reserved || javaDeserializerLocals[id] || javaNestedLocal.MatchString(id)
		}
		if reserved {
			out = append(out, KeywordOverride{Key: "x-" + lang + "-name", Name: id + "_"})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// UpperFirst upper-cases the first letter of s.
func UpperFirst(s string) string {
	r := []rune(s)
	if len(r) > 0 {
		r[0] = unicode.ToUpper(r[0])
	}
	return string(r)
}

func lowerFirst(s string) string {
	r := []rune(s)
	if len(r) > 0 {
		r[0] = unicode.ToLower(r[0])
	}
	return string(r)
}

// OperationKey is the operations key for an RPC: its name in lowerCamelCase,
// which nexgen requires. The RPC name itself stays the wire name, through
// the operation's fqn.
func OperationKey(rpc string) string {
	return lowerFirst(rpc)
}
