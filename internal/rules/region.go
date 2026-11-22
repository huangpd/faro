package rules

import (
	_ "embed"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

//go:embed prelude.rules
var preludeText string

// A region is recognised in a node's name by its flag, by full names, and
// by short codes. Matching is strict on purpose: a node in the wrong
// country is the failure that routes by region exist to prevent.
type region struct {
	id    string
	flags []string // flag emoji
	names []string // full names, matched as substrings, ignoring case
	codes []string // short codes, matched as whole upper-case words
}

// defs is what the text defines about names: regions, and which names are
// notices. The built-in set is data (prelude.rules); a user's text adds to a
// copy of it.
type defs struct {
	regions map[string]*region
	order   []string // region ids in the order defined: the order they are listed in
	notices []*regexp.Regexp
}

var builtin = mustBuiltin()

func mustBuiltin() *defs {
	d := &defs{regions: map[string]*region{}}
	for i, raw := range strings.Split(preludeText, "\n") {
		f := fields(stripComment(raw))
		if len(f) == 0 {
			continue
		}
		if ok, err := d.apply(f); !ok || err != "" {
			panic(fmt.Sprintf("prelude.rules line %d: %s", i+1, err))
		}
	}
	return d
}

// clone copies the definitions, so that a text's own do not reach others.
func (d *defs) clone() *defs {
	c := &defs{regions: make(map[string]*region, len(d.regions)), order: append([]string(nil), d.order...), notices: append([]*regexp.Regexp(nil), d.notices...)}
	for k, v := range d.regions {
		c.regions[k] = v
	}
	return c
}

var regionIDRe = regexp.MustCompile(`^[A-Z][A-Z0-9]{0,7}$`)

// apply reads a "region" or "notice" statement; handled is false for any
// other statement. A region with a known id replaces it.
func (d *defs) apply(f []string) (handled bool, err string) {
	switch f[0] {
	case "region":
		if len(f) < 3 {
			return true, `expected: region <ID> [flag:<emoji>] [name:<text>] [code:<XX>] ...`
		}
		if !regionIDRe.MatchString(f[1]) {
			return true, fmt.Sprintf("region id %q: use upper-case letters and digits, starting with a letter", f[1])
		}
		r := &region{id: f[1]}
		for _, tok := range f[2:] {
			kind, val, ok := strings.Cut(tok, ":")
			if !ok || val == "" {
				return true, fmt.Sprintf("%q: expected flag:, name: or code:", tok)
			}
			switch kind {
			case "flag":
				r.flags = append(r.flags, val)
			case "name":
				r.names = append(r.names, strings.ToLower(val))
			case "code":
				r.codes = append(r.codes, val)
			default:
				return true, fmt.Sprintf("%q: expected flag:, name: or code:", tok)
			}
		}
		if d.regions[r.id] == nil { // a replaced region keeps its place
			d.order = append(d.order, r.id)
		}
		d.regions[r.id] = r
		return true, ""
	case "notice":
		if len(f) != 2 {
			return true, `expected: notice "<regular expression>"`
		}
		re, e := regexp.Compile(f[1])
		if e != nil {
			return true, "bad regular expression: " + e.Error()
		}
		d.notices = append(d.notices, re)
		return true, ""
	}
	return false, ""
}

// regionIDs lists the regions, sorted, for messages.
func (d *defs) regionIDs() []string {
	ids := make([]string, 0, len(d.regions))
	for id := range d.regions {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// regionOf is the first region, in the order they are defined, that a node's
// name says it is in; "" if none does or it is a notice.
func (d *defs) regionOf(name string) string {
	for _, id := range d.order {
		if d.inRegion(name, id) {
			return id
		}
	}
	return ""
}

// label is how a region is shown: its name in the first language that is not
// the code (a name with Chinese characters first, as the users of this text
// read them), then the code: "日本 · JP". A region with only a code is the code.
func (d *defs) label(id string) string {
	r := d.regions[id]
	if r == nil {
		return id
	}
	for _, n := range r.names {
		for _, c := range n {
			if c >= 0x4e00 && c <= 0x9fff {
				return n + " · " + id
			}
		}
	}
	if len(r.names) > 0 {
		return strings.ToUpper(r.names[0][:1]) + r.names[0][1:] + " · " + id
	}
	return id
}

func (d *defs) isNotice(name string) bool {
	for _, re := range d.notices {
		if re.MatchString(name) {
			return true
		}
	}
	return false
}

// tokens splits a name at anything that is not a letter or a digit.
func tokens(name string) []string {
	return strings.FieldsFunc(name, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
}

func (d *defs) inRegion(name, id string) bool {
	r := d.regions[strings.ToUpper(id)]
	if r == nil || d.isNotice(name) {
		return false
	}
	for _, f := range r.flags {
		if strings.Contains(name, f) {
			return true
		}
	}
	low := strings.ToLower(name)
	for _, n := range r.names {
		if strings.Contains(low, n) {
			return true
		}
	}
	for _, tok := range tokens(name) {
		for _, c := range r.codes {
			if tok == c { // whole token, upper case as written
				return true
			}
		}
	}
	return false
}

// RegionOf is the built-in region a node's name says it is in, "" for none.
func RegionOf(name string) string { return builtin.regionOf(name) }

// RegionLabel is how a built-in region is shown: "日本 · JP".
func RegionLabel(id string) string { return builtin.label(id) }

// RegionIDs lists the built-in regions, sorted.
func RegionIDs() []string { return builtin.regionIDs() }

// IsNotice reports whether a node name is a notice by the built-in patterns.
func IsNotice(name string) bool { return builtin.isNotice(name) }

// InRegion reports whether a node name says it is in the built-in region.
func InRegion(name, id string) bool { return builtin.inRegion(name, id) }

// IsNotice reports whether a name is a notice by this plan's patterns, which
// are the built-in ones and the text's own.
func (p *Plan) IsNotice(name string) bool {
	if p == nil || p.defs == nil {
		return IsNotice(name)
	}
	return p.defs.isNotice(name)
}

// RegionOf is the region a node's name says it is in, by this plan's
// definitions, "" for none.
func (p *Plan) RegionOf(name string) string {
	if p == nil || p.defs == nil {
		return RegionOf(name)
	}
	return p.defs.regionOf(name)
}

// RegionOrder lists the regions in the order they were defined: the order to
// show them in.
func (p *Plan) RegionOrder() []string {
	if p == nil || p.defs == nil {
		return append([]string(nil), builtin.order...)
	}
	return append([]string(nil), p.defs.order...)
}

// RegionLabel is how a region is shown by this plan's definitions: "日本 · JP".
func (p *Plan) RegionLabel(id string) string {
	if p == nil || p.defs == nil {
		return RegionLabel(id)
	}
	return p.defs.label(id)
}

// Regions lists the regions this plan knows, sorted.
func (p *Plan) Regions() []string {
	if p == nil || p.defs == nil {
		return RegionIDs()
	}
	return p.defs.regionIDs()
}
