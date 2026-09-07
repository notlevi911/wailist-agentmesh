package nodes

import (
	"bytes"
	"crypto/hmac"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"hash"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/agentmesh/backend/internal/models"
	"github.com/andybalholm/cascadia"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
)

// executeSet builds an object from the node's `setFields` JSON, expanding
// {{ }} references in every string value. It returns a map rather than a
// string so downstream nodes can address individual fields with
// {{ node.<id>.<field> }}.
func executeSet(node models.WorkflowNode, rc RunContexter) (any, error) {
	raw := configVal(node, "setFields", "")
	if raw == "" {
		return nil, errors.New("set: no fields configured — set `setFields` to a JSON object")
	}
	var fields map[string]any
	if err := json.Unmarshal([]byte(raw), &fields); err != nil {
		return nil, fmt.Errorf("set: `setFields` is not a valid JSON object: %w", err)
	}
	out := make(map[string]any, len(fields))
	for k, v := range fields {
		if s, ok := v.(string); ok {
			out[k] = resolveTemplate(s, rc)
			continue
		}
		out[k] = v
	}
	return out, nil
}

// executeJSONExtract parses the upstream output as JSON and walks a dot path
// into it. Numeric segments index arrays: "data.items.0.name".
//
// Like every other new-in-this-PR handler defaulting to rc.Message() with no
// explicit {{ node.<id> }} source, this inherits engine.RunContext.Message's
// pre-existing, already-documented ambiguity: with more than one direct
// upstream predecessor in the same parallel topological level, which one's
// output this reads is decided by goroutine scheduling, not graph position.
// graph.go's implicit-edge tracking only orders explicit {{ node.<id> }}
// refs; it does not (and can't, without threading the calling node's actual
// predecessor ID through RunContexter -- see Message's doc comment) resolve
// this default-context read path. A workflow relying on this node's exact
// upstream source with more than one direct predecessor should reference it
// explicitly via {{ node.<id> }} instead of the bare/default form.
func executeJSONExtract(node models.WorkflowNode, rc RunContexter) (any, error) {
	path := configVal(node, "jsonPath", "")
	if path == "" {
		return nil, errors.New("json_extract: no `jsonPath` configured")
	}
	doc, err := jsonDocFromOutput(rc.LastOutput())
	if err != nil {
		return nil, fmt.Errorf("json_extract: %w", err)
	}
	v, err := walkPath(doc, path)
	if err != nil {
		return nil, fmt.Errorf("json_extract: %w", err)
	}
	return v, nil
}

// jsonDocFromOutput normalizes an upstream node's raw output into a document
// walkPath can descend. rc.LastOutput() (not rc.Message()) on purpose:
// Message()/anyToString special-cases a map output carrying a "message"
// string field by returning just that inner string, which would make a
// genuinely structured upstream output like {"message":"ok","data":{...}}
// look like the bare, non-JSON string "ok" here and fail with a confusing
// "not valid JSON" error despite the real structure being right there.
//
// A raw JSON-text string (e.g. an "http" tool node's raw response body, or a
// value set via rc.Set with a string) is unmarshaled directly. Anything else
// is round-tripped through json.Marshal/Unmarshal too, not returned as-is:
// walkPath only knows how to descend into map[string]any/[]any, but several
// connectors (fetchRSS, fetchHackerNews) return concrete Go types nested
// inside their output map -- []map[string]any, for one -- which don't match
// walkPath's type switch even though they're structurally identical to
// []any. The old rc.Message()-based path never hit this because
// anyToString's JSON formatting normalized every concrete type down to the
// generic shape first; reading LastOutput() directly bypasses that, so this
// has to do the same normalization itself.
func jsonDocFromOutput(v any) (any, error) {
	if s, ok := v.(string); ok {
		var doc any
		if err := json.Unmarshal([]byte(s), &doc); err != nil {
			return nil, fmt.Errorf("upstream output is not valid JSON: %w", err)
		}
		return doc, nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("upstream output could not be normalized to JSON: %w", err)
	}
	var doc any
	if err := json.Unmarshal(b, &doc); err != nil {
		return nil, fmt.Errorf("upstream output could not be normalized to JSON: %w", err)
	}
	return doc, nil
}

// executeCrypto hashes / encodes the upstream output. Pure stdlib; no network.
//
// md5 and sha1 are included because real third-party APIs still require them
// for signature schemes (e.g. legacy webhook verification). They are not
// offered as a security recommendation.
func executeCrypto(node models.WorkflowNode, rc RunContexter) (any, error) {
	in := rc.Message()
	action := configVal(node, "cryptoAction", "sha256")

	var h hash.Hash
	switch action {
	case "sha256":
		h = sha256.New()
	case "sha512":
		h = sha512.New()
	case "sha1":
		h = sha1.New()
	case "md5":
		h = md5.New()
	case "hmac-sha256":
		secret := secretVal(node, "cryptoSecret")
		if secret == "" {
			return nil, errors.New("crypto: hmac-sha256 needs `cryptoSecret` set")
		}
		h = hmac.New(sha256.New, []byte(secret))
	case "base64":
		return base64.StdEncoding.EncodeToString([]byte(in)), nil
	case "base64decode":
		b, err := base64.StdEncoding.DecodeString(in)
		if err != nil {
			return nil, fmt.Errorf("crypto: input is not valid base64: %w", err)
		}
		return string(b), nil
	default:
		return nil, fmt.Errorf("crypto: unsupported action %q "+
			"(want sha256, sha512, sha1, md5, hmac-sha256, base64, base64decode)", action)
	}
	h.Write([]byte(in))
	return hex.EncodeToString(h.Sum(nil)), nil
}

// executeDateTime renders the current time. With no config it returns
// UTC RFC3339 — byte-identical to the previous hardcoded behaviour, so
// existing workflows using the datetime tool are unaffected.
func executeDateTime(node models.WorkflowNode) (any, error) {
	now := time.Now()

	if off := configVal(node, "dtOffset", ""); off != "" {
		d, err := time.ParseDuration(off)
		if err != nil {
			return nil, fmt.Errorf("datetime: `dtOffset` %q is not a duration (try -24h, 30m): %w", off, err)
		}
		now = now.Add(d)
	}

	loc := time.UTC
	if zone := configVal(node, "dtZone", ""); zone != "" {
		l, err := time.LoadLocation(zone)
		if err != nil {
			return nil, fmt.Errorf("datetime: unknown timezone %q (want an IANA name like Asia/Kolkata): %w", zone, err)
		}
		loc = l
	}
	now = now.In(loc)

	switch f := configVal(node, "dtFormat", "rfc3339"); f {
	case "rfc3339":
		return now.Format(time.RFC3339), nil
	case "unix":
		return strconv.FormatInt(now.Unix(), 10), nil
	case "date":
		return now.Format("2006-01-02"), nil
	case "time":
		return now.Format("15:04:05"), nil
	default:
		// Anything else is treated as a literal Go layout string.
		return now.Format(f), nil
	}
}

// executeXMLToJSON converts the upstream output from XML into a JSON-shaped
// map. Attributes become "@name" keys; repeated child elements collapse into a
// slice; leaf elements become their trimmed character data.
func executeXMLToJSON(rc RunContexter) (any, error) {
	dec := xml.NewDecoder(strings.NewReader(rc.Message()))
	for {
		tok, err := dec.Token()
		if err != nil {
			return nil, fmt.Errorf("xml: could not parse input: %w", err)
		}
		start, ok := tok.(xml.StartElement)
		if !ok {
			continue // skip prolog, comments, leading whitespace
		}
		val, err := decodeXMLNode(dec, start)
		if err != nil {
			return nil, fmt.Errorf("xml: could not parse input: %w", err)
		}
		return val, nil
	}
}

// decodeXMLNode reads one element's subtree. The RSS connector parses via
// its own typed xml.Unmarshal instead of this generic tree-walker -- kept
// package-level rather than inlined into executeXMLToJSON only for
// readability, not because anything else currently calls it.
func decodeXMLNode(d *xml.Decoder, start xml.StartElement) (any, error) {
	node := map[string]any{}
	for _, a := range start.Attr {
		node["@"+a.Name.Local] = a.Value
	}
	var text strings.Builder

	for {
		tok, err := d.Token()
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			child, err := decodeXMLNode(d, t)
			if err != nil {
				return nil, err
			}
			key := t.Name.Local
			switch existing := node[key].(type) {
			case nil:
				node[key] = child
			case []any:
				node[key] = append(existing, child)
			default:
				node[key] = []any{existing, child}
			}
		case xml.CharData:
			text.Write(t)
		case xml.EndElement:
			trimmed := strings.TrimSpace(text.String())
			// A leaf with no attributes and no children is just its text.
			if len(node) == 0 {
				return trimmed, nil
			}
			if trimmed != "" {
				node["#text"] = trimmed
			}
			return node, nil
		}
	}
}

// executeTemplate renders free text with {{ }} references expanded.
func executeTemplate(node models.WorkflowNode, rc RunContexter) (any, error) {
	tpl := configVal(node, "templateText", "")
	if tpl == "" {
		return nil, errors.New("template: no `templateText` configured")
	}
	return resolveTemplate(tpl, rc), nil
}

// executeHTMLExtract runs a CSS selector over the upstream output. This parses
// HTML that is already in the run context — it does not fetch a URL. Chain it
// after the `http` tool to scrape a page.
//
// goquery panics on a malformed selector rather than returning an error, so the
// selector is compiled up front with cascadia (goquery's own selector engine)
// where failure is an ordinary error.
func executeHTMLExtract(node models.WorkflowNode, rc RunContexter) (any, error) {
	selector := configVal(node, "htmlSelector", "")
	if selector == "" {
		return nil, errors.New("html_extract: no `htmlSelector` configured")
	}
	sel, err := compileHTMLSelector(selector)
	if err != nil {
		return nil, fmt.Errorf("html_extract: %q is not a valid CSS selector: %w", selector, err)
	}
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(rc.Message()))
	if err != nil {
		return nil, fmt.Errorf("html_extract: could not parse the HTML: %w", err)
	}
	attr := configVal(node, "htmlAttr", "")

	read := func(s *goquery.Selection) string {
		if attr == "" {
			return strings.TrimSpace(s.Text())
		}
		v, _ := s.Attr(attr)
		return v
	}

	matches := doc.FindMatcher(sel)
	if configVal(node, "htmlMode", "first") == "all" {
		// Non-nil even when empty, so downstream range/len never hit a nil.
		out := make([]string, 0, matches.Length())
		matches.Each(func(_ int, s *goquery.Selection) {
			out = append(out, read(s))
		})
		return out, nil
	}
	if matches.Length() == 0 {
		// A missing element is normal on a real page, not a run-halting error.
		return "", nil
	}
	return read(matches.First()), nil
}

// htmlSelectorCacheLimit caps how many distinct compiled selectors are kept.
// A workflow builder trying many ad-hoc selectors shouldn't grow this
// unboundedly — past the cap, selectors are simply compiled fresh each call,
// same as before caching existed.
const htmlSelectorCacheLimit = 256

var (
	htmlSelectorCacheMu sync.Mutex
	htmlSelectorCache   = map[string]cascadia.Selector{}
)

// compileHTMLSelector compiles and caches a CSS selector. cascadia.Compile
// does real parsing work on every call; html_extract nodes reuse the same
// handful of selectors across every run of a workflow, so caching by the
// selector string avoids repeating that work each execution.
func compileHTMLSelector(selector string) (cascadia.Selector, error) {
	htmlSelectorCacheMu.Lock()
	sel, ok := htmlSelectorCache[selector]
	htmlSelectorCacheMu.Unlock()
	if ok {
		return sel, nil
	}

	sel, err := cascadia.Compile(selector)
	if err != nil {
		return nil, err
	}

	htmlSelectorCacheMu.Lock()
	if len(htmlSelectorCache) < htmlSelectorCacheLimit {
		htmlSelectorCache[selector] = sel
	}
	htmlSelectorCacheMu.Unlock()
	return sel, nil
}

// mdRendererGFM and mdRendererPlain are shared goldmark instances — goldmark
// is designed to be constructed once and reused across concurrent Convert
// calls, and its own construction (parser/renderer wiring) is not free
// enough to redo on every node execution. Two fixed instances cover the only
// two configurations mdGFM selects between.
var (
	mdRendererGFM   = goldmark.New(goldmark.WithExtensions(extension.GFM))
	mdRendererPlain = goldmark.New()
)

// executeMarkdown renders the upstream output from Markdown into HTML.
// GitHub Flavored Markdown (tables, strikethrough, autolinks) is on by default
// because that is what LLM output actually looks like.
//
// Raw HTML embedded in the source is NOT passed through — goldmark escapes it
// unless WithUnsafe is set, and it is deliberately not set here: this node's
// input is frequently model output or third-party text, which is exactly the
// content you do not want emitting live HTML into an email.
func executeMarkdown(node models.WorkflowNode, rc RunContexter) (any, error) {
	md := mdRendererPlain
	if configVal(node, "mdGFM", "true") != "false" {
		md = mdRendererGFM
	}

	var buf bytes.Buffer
	if err := md.Convert([]byte(rc.Message()), &buf); err != nil {
		return nil, fmt.Errorf("markdown: render failed: %w", err)
	}
	return buf.String(), nil
}

// quickChartBase is the public QuickChart renderer. This node builds a URL and
// makes NO request — the image is fetched by whoever renders the URL (Slack,
// an email client, a browser). That is why it is a NodeTypeTool (free under
// BillableFlatFee) rather than an action.
const quickChartBase = "https://quickchart.io/chart"

// executeQuickChart returns a QuickChart image URL for a Chart.js config.
func executeQuickChart(node models.WorkflowNode, rc RunContexter) (any, error) {
	// resolveTemplateJSON, not resolveTemplate: qcConfig is raw JSON *text*
	// substituted before parsing, so an unescaped quote in a resolved value
	// (e.g. an LLM reply containing `"`) would otherwise break the JSON --
	// see resolveTemplateJSON's doc comment.
	cfg := resolveTemplateJSON(configVal(node, "qcConfig", ""), rc)
	if cfg == "" {
		return nil, errors.New("quickchart: no `qcConfig` configured — set a Chart.js config object")
	}
	// Validate before handing the user a URL that renders an error image.
	var probe any
	if err := json.Unmarshal([]byte(cfg), &probe); err != nil {
		return nil, fmt.Errorf("quickchart: `qcConfig` is not valid JSON: %w", err)
	}
	q := url.Values{}
	q.Set("c", cfg)
	if w := configVal(node, "qcWidth", ""); w != "" {
		q.Set("w", w)
	}
	if h := configVal(node, "qcHeight", ""); h != "" {
		q.Set("h", h)
	}
	return quickChartBase + "?" + q.Encode(), nil
}
