package nodes

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
)

// stateRef matches exactly `{{state.<path>}}`, with optional inner
// whitespace, where <path> is one or more dot-separated identifier
// segments. Nothing else is a state reference.
//
// The pattern is deliberately strict rather than a general template
// syntax. Workflow fields today are free text — prompts, JSON bodies,
// email bodies — and some of them legitimately contain braces. Anything
// this regex does not match is returned untouched, so adding state support
// cannot change what an existing workflow sends.
var stateRef = regexp.MustCompile(`\{\{\s*state((?:\.[A-Za-z_][A-Za-z0-9_]*)+)\s*\}\}`)

// ExpandState replaces `{{state.key}}` / `{{state.a.b}}` references in s
// with values from a workflow's persisted variables. It is the identity
// function on any string containing no such reference — including a string
// with other `{{...}}` placeholders, which are left for whatever else may
// consume them.
//
// An unresolvable path expands to the empty string rather than leaving the
// placeholder in place: a literal "{{state.x}}" reaching a third-party API
// is worse than an empty value, and it makes "no value yet" the same shape
// as "empty value".
func ExpandState(s string, state map[string]any) string {
	if s == "" || !strings.Contains(s, "{{") {
		return s
	}
	// With no state loaded there is nothing to substitute; leaving the
	// placeholders intact makes the misconfiguration visible instead of
	// silently blanking fields.
	if len(state) == 0 {
		return s
	}
	return stateRef.ReplaceAllStringFunc(s, func(match string) string {
		m := stateRef.FindStringSubmatch(match)
		if len(m) != 2 {
			return match
		}
		path := strings.Split(strings.TrimPrefix(m[1], "."), ".")
		v, ok := lookupState(state, path)
		if !ok {
			return ""
		}
		return stateValueString(v)
	})
}

// lookupState walks a dotted path through nested maps.
func lookupState(state map[string]any, path []string) (any, bool) {
	var cur any = state
	for _, seg := range path {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		cur, ok = m[seg]
		if !ok {
			return nil, false
		}
	}
	return cur, true
}

// stateValueString renders a resolved value for interpolation: strings
// verbatim, numbers without a trailing ".0", and anything structured as
// compact JSON.
func stateValueString(v any) string {
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
	default:
		b, err := json.Marshal(t)
		if err != nil {
			return ""
		}
		return string(b)
	}
}
