// Package emit writes definition documents as deterministic YAML or JSON.
//
// A document is a tree of Map, List, and scalar values (string, int64, bool).
// Key order is the order the caller built, so the same tree always encodes to
// the same bytes.
package emit

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Map is an ordered mapping.
type Map struct {
	Entries []Entry
	// Flow keeps the mapping on one line in YAML.
	Flow bool
}

// Entry is one key of a Map.
type Entry struct {
	Key   string
	Value any
}

// List is an ordered sequence.
type List struct {
	Items []any
	// Flow keeps the sequence on one line in YAML.
	Flow bool
}

// Quoted is a string that is always double-quoted in YAML, such as a $ref or
// a version number that would otherwise read as a float.
type Quoted string

// Set appends key with value.
func (m *Map) Set(key string, value any) {
	m.Entries = append(m.Entries, Entry{Key: key, Value: value})
}

// Format is an output encoding.
type Format string

const (
	YAML Format = "yaml"
	JSON Format = "json"
)

// ParseFormat validates an out_format option value.
func ParseFormat(s string) (Format, error) {
	switch Format(s) {
	case "", YAML:
		return YAML, nil
	case JSON:
		return JSON, nil
	}
	return "", fmt.Errorf("out_format must be yaml or json, got %q", s)
}

// Ext is the file extension for f, without the dot.
func (f Format) Ext() string { return string(f) }

// Encode renders doc in format f. header lines are written as YAML comments;
// JSON has no comments, so they are dropped.
func Encode(f Format, header []string, doc *Map) ([]byte, error) {
	if f == JSON {
		return encodeJSON(doc)
	}
	return encodeYAML(header, doc)
}

func encodeYAML(header []string, doc *Map) ([]byte, error) {
	root := yamlNode(doc)
	if len(header) > 0 {
		root.HeadComment = strings.Join(header, "\n")
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{root}}); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func yamlNode(v any) *yaml.Node {
	switch v := v.(type) {
	case *Map:
		n := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		if v.Flow {
			n.Style = yaml.FlowStyle
		}
		for _, e := range v.Entries {
			n.Content = append(n.Content, yamlNode(e.Key), yamlNode(e.Value))
		}
		return n
	case *List:
		n := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		if v.Flow {
			n.Style = yaml.FlowStyle
		}
		for _, item := range v.Items {
			n.Content = append(n.Content, yamlNode(item))
		}
		return n
	case Quoted:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: string(v), Style: yaml.DoubleQuotedStyle}
	case string:
		n := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v}
		if strings.Contains(v, "\n") {
			n.Style = yaml.LiteralStyle
		}
		return n
	case int64:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: strconv.FormatInt(v, 10)}
	case bool:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: strconv.FormatBool(v)}
	}
	panic(fmt.Sprintf("emit: unsupported value %T", v))
}

func encodeJSON(doc *Map) ([]byte, error) {
	var buf bytes.Buffer
	if err := writeJSON(&buf, doc, ""); err != nil {
		return nil, err
	}
	buf.WriteByte('\n')
	return buf.Bytes(), nil
}

func writeJSON(buf *bytes.Buffer, v any, indent string) error {
	inner := indent + "  "
	switch v := v.(type) {
	case *Map:
		if len(v.Entries) == 0 {
			buf.WriteString("{}")
			return nil
		}
		buf.WriteString("{\n")
		for i, e := range v.Entries {
			buf.WriteString(inner)
			if err := writeJSON(buf, e.Key, inner); err != nil {
				return err
			}
			buf.WriteString(": ")
			if err := writeJSON(buf, e.Value, inner); err != nil {
				return err
			}
			if i < len(v.Entries)-1 {
				buf.WriteByte(',')
			}
			buf.WriteByte('\n')
		}
		buf.WriteString(indent + "}")
	case *List:
		if len(v.Items) == 0 {
			buf.WriteString("[]")
			return nil
		}
		buf.WriteString("[\n")
		for i, item := range v.Items {
			buf.WriteString(inner)
			if err := writeJSON(buf, item, inner); err != nil {
				return err
			}
			if i < len(v.Items)-1 {
				buf.WriteByte(',')
			}
			buf.WriteByte('\n')
		}
		buf.WriteString(indent + "]")
	case Quoted:
		return writeJSON(buf, string(v), indent)
	case string, int64, bool:
		var b bytes.Buffer
		enc := json.NewEncoder(&b)
		enc.SetEscapeHTML(false)
		if err := enc.Encode(v); err != nil {
			return err
		}
		buf.Write(bytes.TrimSuffix(b.Bytes(), []byte("\n")))
	default:
		return fmt.Errorf("emit: unsupported value %T", v)
	}
	return nil
}
