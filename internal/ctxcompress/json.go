package ctxcompress

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
)

// jsonValue is a decoded JSON value that remembers object key order, so a
// rewrite changes only what it means to change.
type jsonValue struct {
	kind   byte // 'o' object, 'a' array, 's' string, 'n' number, 'b' bool, 'z' null
	keys   []string
	fields []*jsonValue
	items  []*jsonValue
	str    string
	raw    string // number literal, or "true"/"false"/"null"
}

func parseJSON(text string) (*jsonValue, error) {
	decoder := json.NewDecoder(strings.NewReader(text))
	decoder.UseNumber()
	value, err := decodeJSONValue(decoder)
	if err != nil {
		return nil, err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil, fmt.Errorf("trailing data after JSON value")
	}
	return value, nil
}

func decodeJSONValue(decoder *json.Decoder) (*jsonValue, error) {
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	switch t := token.(type) {
	case json.Delim:
		switch t {
		case '{':
			value := &jsonValue{kind: 'o'}
			for decoder.More() {
				keyToken, err := decoder.Token()
				if err != nil {
					return nil, err
				}
				key, _ := keyToken.(string)
				field, err := decodeJSONValue(decoder)
				if err != nil {
					return nil, err
				}
				value.keys = append(value.keys, key)
				value.fields = append(value.fields, field)
			}
			_, err := decoder.Token()
			return value, err
		case '[':
			value := &jsonValue{kind: 'a'}
			for decoder.More() {
				item, err := decodeJSONValue(decoder)
				if err != nil {
					return nil, err
				}
				value.items = append(value.items, item)
			}
			_, err := decoder.Token()
			return value, err
		}
	case string:
		return &jsonValue{kind: 's', str: t}, nil
	case json.Number:
		return &jsonValue{kind: 'n', raw: t.String()}, nil
	case bool:
		return &jsonValue{kind: 'b', raw: fmt.Sprint(t)}, nil
	case nil:
		return &jsonValue{kind: 'z', raw: "null"}, nil
	}
	return nil, fmt.Errorf("unexpected JSON token %v", token)
}

func (v *jsonValue) field(key string) *jsonValue {
	for i, k := range v.keys {
		if k == key {
			return v.fields[i]
		}
	}
	return nil
}

func (v *jsonValue) encode(buf *bytes.Buffer) {
	switch v.kind {
	case 'o':
		buf.WriteByte('{')
		for i, key := range v.keys {
			if i > 0 {
				buf.WriteByte(',')
			}
			writeJSONString(buf, key)
			buf.WriteByte(':')
			v.fields[i].encode(buf)
		}
		buf.WriteByte('}')
	case 'a':
		buf.WriteByte('[')
		for i, item := range v.items {
			if i > 0 {
				buf.WriteByte(',')
			}
			item.encode(buf)
		}
		buf.WriteByte(']')
	case 's':
		writeJSONString(buf, v.str)
	default:
		buf.WriteString(v.raw)
	}
}

func (v *jsonValue) compact() string {
	var buf bytes.Buffer
	v.encode(&buf)
	return buf.String()
}

func writeJSONString(buf *bytes.Buffer, s string) {
	encoder := json.NewEncoder(buf)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(s)
	buf.Truncate(buf.Len() - 1) // Encode appends a newline.
}

// compressJSON handles a tool result that is one JSON document. It always
// minifies. A top-level array of records is rendered as a table with the
// field names written once, Headroom's CSV-with-schema form; with sampling
// enabled, long arrays anywhere in the document keep only a representative
// subset first.
func compressJSON(text string, opts Options) (string, bool) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" || (trimmed[0] != '{' && trimmed[0] != '[') {
		return "", false
	}
	value, err := parseJSON(trimmed)
	if err != nil {
		return "", false
	}
	if value.kind != 'a' {
		if opts.Sample {
			sampleJSON(value)
		}
		return value.compact(), true
	}

	// A top-level array keeps its omitted-items note outside the table.
	items, total := value.items, len(value.items)
	if opts.Sample {
		for _, item := range items {
			sampleJSON(item)
		}
		items, total = sampleArray(items)
	}
	note := ""
	if len(items) < total {
		note = fmt.Sprintf(omittedItemsFormat, total-len(items), total)
	}
	if table, ok := renderTable(items); ok {
		if note != "" {
			table += "\n" + note
		}
		return table, true
	}
	if note != "" {
		items = append(items, &jsonValue{kind: 's', str: note})
	}
	return (&jsonValue{kind: 'a', items: items}).compact(), true
}

// isTableHeader recognises a rendered table, so that compressing it a
// second time leaves it alone.
func isTableHeader(line string) bool {
	if !strings.HasPrefix(line, "[") {
		return false
	}
	end := strings.Index(line, "]{")
	if end < 2 || !strings.HasSuffix(line, "}") {
		return false
	}
	for _, c := range line[1:end] {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

const (
	tableMinRows       = 3
	tableCoreFrequency = 0.8
	tableCoreRatio     = 0.6
)

// renderTable writes an array of objects as
//
//	[N]{name:type,name:type?,...}
//	cell,cell,...
//
// with one row per item. It declines when the items are not objects or do
// not share most of their keys, since a sparse table saves nothing.
func renderTable(items []*jsonValue) (string, bool) {
	if len(items) < tableMinRows {
		return "", false
	}
	frequency := map[string]int{}
	var order []string
	for _, item := range items {
		if item.kind != 'o' {
			return "", false
		}
		for _, key := range item.keys {
			if key == "" || strings.ContainsAny(key, ",:{}[]\"\n\r") {
				return "", false
			}
			if frequency[key] == 0 {
				order = append(order, key)
			}
			frequency[key]++
		}
	}
	if len(order) == 0 {
		return "", false
	}
	threshold := int(float64(len(items))*tableCoreFrequency + 0.999)
	core := 0
	for _, key := range order {
		if frequency[key] >= threshold {
			core++
		}
	}
	if float64(core) < float64(len(order))*tableCoreRatio {
		return "", false
	}
	// Frequent columns first; among equals, the order keys first appeared in.
	columns := append([]string(nil), order...)
	sort.SliceStable(columns, func(i, j int) bool { return frequency[columns[i]] > frequency[columns[j]] })

	var buf bytes.Buffer
	fmt.Fprintf(&buf, "[%d]{", len(items))
	for i, column := range columns {
		if i > 0 {
			buf.WriteByte(',')
		}
		buf.WriteString(column)
		buf.WriteByte(':')
		buf.WriteString(columnType(items, column))
		if columnNullable(items, column) {
			buf.WriteByte('?')
		}
	}
	buf.WriteString("}")
	for _, item := range items {
		buf.WriteByte('\n')
		for i, column := range columns {
			if i > 0 {
				buf.WriteByte(',')
			}
			if cell := item.field(column); cell != nil {
				buf.WriteString(tableCell(cell))
			}
		}
	}
	return buf.String(), true
}

func columnType(items []*jsonValue, column string) string {
	kind := byte(0)
	for _, item := range items {
		cell := item.field(column)
		if cell == nil || cell.kind == 'z' {
			continue
		}
		if kind == 0 {
			kind = cell.kind
		} else if kind != cell.kind {
			return "json"
		}
	}
	switch kind {
	case 'n':
		return "number"
	case 'b':
		return "bool"
	case 'o', 'a':
		return "json"
	default:
		return "string"
	}
}

func columnNullable(items []*jsonValue, column string) bool {
	for _, item := range items {
		if cell := item.field(column); cell == nil || cell.kind == 'z' {
			return true
		}
	}
	return false
}

// tableCell renders one value CSV-style: bare when unambiguous, quoted with
// doubled quotes otherwise. Nested objects and arrays stay compact JSON.
func tableCell(cell *jsonValue) string {
	var text string
	switch cell.kind {
	case 's':
		text = cell.str
		if text != "" && text != "null" && !strings.ContainsAny(text, ",\"\n\r") {
			return text
		}
	case 'o', 'a':
		text = cell.compact()
	default:
		return cell.raw
	}
	return `"` + strings.ReplaceAll(text, `"`, `""`) + `"`
}
