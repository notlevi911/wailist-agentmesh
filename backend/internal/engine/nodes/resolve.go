package nodes

import (
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"strconv"
	"strings"
)

// templateRef matches {{ ref }} with optional surrounding whitespace. The ref
// charset deliberately excludes braces and spaces so an unclosed or malformed
// reference simply fails to match and is left in the output verbatim.
var templateRef = regexp.MustCompile(`\{\{\s*([A-Za-z0-9_.\-]+)\s*\}\}`)

// resolveTemplate is the one template engine every connector uses -- both the
// pre-existing "messageTemplate" config field (via resolveMessage in
// connector_helpers.go, used by 20+ connectors that predate this resolver)
// and every field a connector resolves directly against an individual config
// value. It expands {{ }} references in s against the run context:
//
//	{{ result }}              the most recent node output (rc.Message)
//	{{ result.field.path }}   a dotted path into the most recent output
//	{{ input }}               the original trigger input (rc.UserInput)
//	{{ node.<id> }}           that node's output, stringified
//	{{ node.<id>.field.path}} a dotted path into that node's output
//
// A "result"/"result.field" ref that can't resolve (no field of that name,
// or the output isn't a JSON object at that point) expands to "" -- this
// predates node.<id> refs and 20+ shipped connectors' tests already pin it,
// so it's kept exactly as before rather than folded into the rule below.
//
// A "node.<id>" or "node.<id>.field" ref that can't resolve (unknown node,
// unknown field, or the node hasn't run yet) is left verbatim instead.
// Blanking it would produce a silently empty Slack message, email body, or
// Postgres column with no trace of what went wrong; a literal
// "{{ node.n7.city }}" showing up in the output is at least diagnosable.
// The one real way this fires is a race, not a typo: two nodes with no flow
// edge between them can land in the same topological level and run
// concurrently, so a node.<id> ref to a same-level sibling may or may not
// have a value yet depending on goroutine scheduling. BuildAttachMap in
// graph.go adds an implicit dependency edge for every node.<id> ref it finds
// in a template-eligible config field specifically to close that gap for the
// common case; this verbatim fallback is what a genuinely malformed or
// stale reference (typo'd ID, node deleted, or a ref the implicit-edge scan
// doesn't reach) degrades to instead of silently vanishing.
func resolveTemplate(s string, rc RunContexter) string {
	if s == "" || !strings.Contains(s, "{{") {
		return s
	}
	// FindAllStringSubmatchIndex captures both the full match and the ref
	// group's byte offsets in one pass, so each reference is matched exactly
	// once — ReplaceAllStringFunc's callback would otherwise re-run the same
	// regex against text the outer call already matched.
	matches := templateRef.FindAllStringSubmatchIndex(s, -1)
	if matches == nil {
		return s
	}
	var b strings.Builder
	last := 0
	for _, m := range matches {
		start, end, refStart, refEnd := m[0], m[1], m[2], m[3]
		b.WriteString(s[last:start])
		if val, ok := lookupRef(s[refStart:refEnd], rc); ok {
			b.WriteString(val)
		} else {
			b.WriteString(s[start:end])
		}
		last = end
	}
	b.WriteString(s[last:])
	return b.String()
}

func lookupRef(ref string, rc RunContexter) (string, bool) {
	if ref == "result" {
		return rc.Message(), true
	}
	if ref == "input" {
		return rc.UserInput(), true
	}
	if path, ok := strings.CutPrefix(ref, "result."); ok {
		// Always "resolved" (blanks on a miss) -- see resolveTemplate's doc
		// comment for why this ref form keeps its pre-existing behavior.
		v, _ := walkPath(rc.LastOutput(), path)
		return stringifyRef(v), true // blanks on a miss regardless of the error
	}
	rest, isNode := strings.CutPrefix(ref, "node.")
	if !isNode || rest == "" {
		return "", false
	}
	nodeID, field, hasField := strings.Cut(rest, ".")
	out, ok := rc.Get(nodeID)
	if !ok {
		return "", false
	}
	if !hasField {
		return stringifyRef(out), true
	}
	v, err := walkPath(out, field)
	if err != nil {
		return "", false
	}
	return stringifyRef(v), true
}

// resolveTemplateJSON is like resolveTemplate, but every resolved {{ }} ref
// is JSON-encoded (quoted and escaped) before splicing into s, instead of
// substituted as raw text.
//
// Every other resolveTemplate call site assigns its result into a Go value
// (a map field, a struct field) that gets json.Marshal'd afterward, so an
// unescaped quote in a resolved value is harmless there -- encoding/json
// escapes it correctly at marshal time regardless of what the string
// contains. This is for the one call site (quickchart's Chart.js config)
// that substitutes directly into already-serialized JSON *text* before
// parsing it, so an unescaped quote in the resolved value (e.g. an LLM
// reply like `He said "hi"`, dropped into `"label":"{{ result }}"`) breaks
// the JSON outright instead of producing a slightly wrong chart.
//
// Scoped to the common case of a ref sitting inside an existing pair of
// JSON string quotes (the realistic usage for a chart label/title built
// from upstream text) -- not a general "splice a raw JSON substructure"
// mechanism. A ref resolving to an object/array still lands here as
// stringifyRef's flattened JSON text, so it becomes an escaped *string*
// containing that JSON, not a spliced-in substructure.
func resolveTemplateJSON(s string, rc RunContexter) string {
	if s == "" || !strings.Contains(s, "{{") {
		return s
	}
	matches := templateRef.FindAllStringSubmatchIndex(s, -1)
	if matches == nil {
		return s
	}
	var b strings.Builder
	last := 0
	for _, m := range matches {
		start, end, refStart, refEnd := m[0], m[1], m[2], m[3]
		b.WriteString(s[last:start])
		val, ok := lookupRef(s[refStart:refEnd], rc)
		if !ok {
			b.WriteString(s[start:end])
			last = end
			continue
		}
		encoded, err := json.Marshal(val)
		if err != nil || len(encoded) < 2 {
			b.WriteString(s[start:end])
		} else {
			// encoded is a quoted JSON string literal ("..."); strip the
			// surrounding quotes so splicing it inside an existing
			// "label":"{{ ref }}" position yields one correctly-escaped
			// string, not a doubly-quoted one.
			b.Write(encoded[1 : len(encoded)-1])
		}
		last = end
	}
	b.WriteString(s[last:])
	return b.String()
}

// walkPath descends a dotted field path ("a.b.c", or "a.0.c" to index into an
// array) into v. v is JSON-SHAPED but not necessarily json.Unmarshal OUTPUT:
// it may equally be a connector's own concrete Go return value (see
// walkOneSegment for why that distinction is load-bearing rather than
// pedantic). Shared by the {{ }} template resolver here (lookupRef, which
// only cares whether it succeeded) and the JSON Extract node (compute.go's
// executeJSONExtract, which surfaces the specific error to the user) -- one
// traversal implementation for both, so they can never silently diverge on
// what path syntax each accepts. Returns a descriptive error naming exactly
// which segment failed and why if any segment is missing, an array index is
// out of range or non-numeric, or v isn't an object/array at that point;
// callers decide separately what to do with a failure (blank it, leave the
// reference verbatim, or surface the error as-is).
func walkPath(v any, path string) (any, error) {
	cur := v
	for _, key := range strings.Split(path, ".") {
		next, err := walkOneSegment(cur, path, key)
		if err != nil {
			return nil, err
		}
		cur = next
	}
	return cur, nil
}

// walkOneSegment descends exactly one segment of a dotted path into cur.
//
// The map[string]any fast path covers everything that came from
// json.Unmarshal (the common case). Everything else goes through reflect
// rather than a type switch, because a Go type switch matches EXACT types,
// not structurally, and connectors hand back concrete Go types rather than
// the generic shapes json.Unmarshal produces: fetchRSS/fetchHackerNews
// (connectors_feed.go) both return
// map[string]any{"items": []map[string]any{...}}, so a `case []any` never
// matches their "items" and the path would fail as if it hit a scalar.
//
// Reflection rather than enumerating the concrete types connectors happen
// to use today: a future connector returning []map[string]string, []string,
// or a named slice/map type works with no further change here, and neither
// caller (executeJSONExtract's jsonPath or resolveTemplate's
// {{ result.a.b }} refs) can silently diverge from the other on what it
// accepts.
//
// Scope is deliberately maps (string-keyed), slices, and arrays -- NOT
// structs or pointers. A connector returning []SomeStruct therefore gets a
// path that half-works ("items.0" resolves, "items.0.title" then fails as
// a scalar). That's accepted rather than fixed here because every node
// output in this package is already JSON-shaped by construction (it has to
// survive Set/LastOutput and, for most connectors, a real JSON round trip),
// so struct support would be dead code today; adding it would also mean
// picking a field-name convention (exported names vs `json` tags) with no
// caller to validate that choice against. Revisit when a connector
// actually returns structs.
func walkOneSegment(cur any, path, key string) (any, error) {
	if m, ok := cur.(map[string]any); ok {
		next, ok := m[key]
		if !ok {
			return nil, fmt.Errorf("no value at path %q (missing key %q)", path, key)
		}
		return next, nil
	}

	rv := reflect.ValueOf(cur)
	switch rv.Kind() {
	case reflect.Map:
		// Only string-keyed maps are addressable by a dotted path segment.
		if rv.Type().Key().Kind() != reflect.String {
			return nil, fmt.Errorf("path %q descends past a scalar at %q", path, key)
		}
		val := rv.MapIndex(reflect.ValueOf(key).Convert(rv.Type().Key()))
		if !val.IsValid() {
			return nil, fmt.Errorf("no value at path %q (missing key %q)", path, key)
		}
		return val.Interface(), nil
	case reflect.Slice, reflect.Array:
		// A byte slice is JSON-shaped as a string (or a raw document, for
		// json.RawMessage), never an indexable array of numbers. Widening
		// from `case []any` to reflect.Slice newly matches these, so
		// without this guard "{{ result.0 }}" against a []byte output would
		// interpolate a byte's numeric value ('a' -> 97) instead of leaving
		// the ref alone, and a json.RawMessage path would report the
		// misleading "indexes an array with non-numeric segment". Elem
		// kind, not a []byte type assertion: that would miss
		// json.RawMessage and every other named byte-slice type.
		if rv.Type().Elem().Kind() == reflect.Uint8 {
			break
		}
		i, err := strconv.Atoi(key)
		if err != nil {
			return nil, fmt.Errorf("path %q indexes an array with non-numeric segment %q", path, key)
		}
		if i < 0 || i >= rv.Len() {
			return nil, fmt.Errorf("index %d out of range at path %q (length %d)", i, path, rv.Len())
		}
		return rv.Index(i).Interface(), nil
	}
	return nil, fmt.Errorf("path %q descends past a scalar at %q", path, key)
}

// stringifyRef renders a resolved value for interpolation into text. Strings
// pass through unquoted; numbers render without json.Marshal's float
// formatting surprises; everything else falls back to compact JSON.
func stringifyRef(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case bool:
		return strconv.FormatBool(t)
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case int:
		return strconv.Itoa(t)
	case int64:
		return strconv.FormatInt(t, 10)
	}
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}
