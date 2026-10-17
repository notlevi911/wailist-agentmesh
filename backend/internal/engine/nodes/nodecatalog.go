package nodes

import (
	_ "embed"
	"encoding/json"
	"fmt"
)

// nodecatalog.json lists every node type, template and field the canvas
// offers. It is GENERATED -- do not edit it by hand. The source is
// frontend/src/lib/nodeCatalog.ts, which assembles it from the palette and
// connector tables the canvas itself renders; regenerate with
// `npm run gen:node-catalog` in frontend/. The frontend's sync test fails if
// this copy is stale, and nodecatalog_test.go fails if it names a template or
// setting the engine does not actually implement.
//
//go:embed nodecatalog.json
var nodeCatalogJSON []byte

// CatalogField is one setting on a node. Where says where the value lives on
// the saved node and so whether the builder may set it: "field" and
// "config" are settable; "secret" and "connection" are the user's to supply.
type CatalogField struct {
	Key         string `json:"key"`
	Where       string `json:"where"`
	Label       string `json:"label"`
	Hint        string `json:"hint,omitempty"`
	Placeholder string `json:"placeholder,omitempty"`
}

type CatalogTemplate struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Desc string `json:"desc"`
	// Kind is "read", "compute" or "action". Only a read's failure may be
	// degraded into an error payload the run continues with: it fetches from
	// a source outside the workflow, and that source can be down now and back
	// later. A compute template runs entirely in-process, so its failure is a
	// fault in the workflow that would repeat on every run; an action's may
	// already have had an effect outside the workflow. Both fail the run.
	// Generated from frontend/src/lib/nodeCatalog.ts -- see IsDegradable.
	Kind          string            `json:"kind"`
	Note          string            `json:"note,omitempty"`
	Presets       map[string]string `json:"presets,omitempty"`
	Fields        []CatalogField    `json:"fields"`
	AuthDocURL    string            `json:"authDocUrl,omitempty"`
	OAuthProvider string            `json:"oauthProvider,omitempty"`
}

type CatalogType struct {
	Type      string            `json:"type"`
	Desc      string            `json:"desc"`
	Templates []CatalogTemplate `json:"templates"`
}

type NodeCatalog struct {
	Version int           `json:"version"`
	Types   []CatalogType `json:"types"`
}

var nodeCatalog = mustLoadNodeCatalog()

// mustLoadNodeCatalog panics on a malformed file rather than degrading: an
// empty catalog would let the builder create nothing at all, and the file is
// embedded, so a bad one can only come from a bad commit -- which the tests
// in this package catch long before a deploy.
func mustLoadNodeCatalog() NodeCatalog {
	var c NodeCatalog
	if err := json.Unmarshal(nodeCatalogJSON, &c); err != nil {
		panic(fmt.Sprintf("nodecatalog.json: %v", err))
	}
	return c
}

// NodeCatalogData returns the loaded catalog.
func NodeCatalogData() NodeCatalog { return nodeCatalog }

func catalogType(nodeType string) (CatalogType, bool) {
	for _, t := range nodeCatalog.Types {
		if t.Type == nodeType {
			return t, true
		}
	}
	return CatalogType{}, false
}

func catalogTemplate(nodeType, template string) (CatalogTemplate, bool) {
	t, ok := catalogType(nodeType)
	if !ok {
		return CatalogTemplate{}, false
	}
	for _, tpl := range t.Templates {
		if tpl.ID == template {
			return tpl, true
		}
	}
	return CatalogTemplate{}, false
}

// field returns the named field of this template, if it has one.
func (t CatalogTemplate) field(key string) (CatalogField, bool) {
	for _, f := range t.Fields {
		if f.Key == key {
			return f, true
		}
	}
	return CatalogField{}, false
}

// keysWithExamples is keysWhere with each key's placeholder appended as a
// short example -- "jsonPath (e.g. data.items.0.name)" -- because a key's
// expected format, not its name, is what the model gets wrong. Long
// placeholders are left out to keep the prompt compact.
func (t CatalogTemplate) keysWithExamples(where string) []string {
	var out []string
	for _, f := range t.Fields {
		if f.Where != where {
			continue
		}
		if f.Placeholder != "" && len(f.Placeholder) <= 40 {
			out = append(out, fmt.Sprintf("%s (e.g. %s)", f.Key, f.Placeholder))
		} else {
			out = append(out, f.Key)
		}
	}
	return out
}

// keysWhere lists this template's field keys that live in where.
func (t CatalogTemplate) keysWhere(where string) []string {
	var out []string
	for _, f := range t.Fields {
		if f.Where == where {
			out = append(out, f.Key)
		}
	}
	return out
}

// exampleConfigKeys are the config keys whose placeholder earns its bytes in
// the prompt: the ones a build gets wrong in a way an example prevents -- a
// path syntax, a template, a date or range format. Every other config key is
// listed by name only, and describe_node has the full detail for the two or
// three templates a build actually uses.
// Every key here must have a placeholder in the catalog, or keeping it buys
// nothing (httpBodyTemplate and messageTemplate have none, so listing them
// was a no-op). dtOffset and graphqlVariables earn their place: a wrong
// offset or a malformed variables object fails at run time, not at build
// time. TestExampleConfigKeysAllHavePlaceholders enforces both halves.
var exampleConfigKeys = map[string]bool{
	"jsonPath": true, "sheetsRange": true, "calendarStart": true,
	"calendarEnd": true, "graphqlQuery": true, "graphqlVariables": true,
	"dtFormat": true, "dtOffset": true,
}

// keysWhereAnnotated is keysWhere with "(e.g. ...)" appended for the keys in
// withExample only.
func (t CatalogTemplate) keysWhereAnnotated(where string, withExample map[string]bool) []string {
	var out []string
	for _, f := range t.Fields {
		if f.Where != where {
			continue
		}
		if withExample[f.Key] && f.Placeholder != "" && len(f.Placeholder) <= 40 {
			out = append(out, fmt.Sprintf("%s (e.g. %s)", f.Key, f.Placeholder))
			continue
		}
		out = append(out, f.Key)
	}
	return out
}
