// Package applib is the application library: well-known services and what
// their traffic is, to be added to an exit in one step. The entries are data
// (data/apps.list), not code.
package applib

import (
	_ "embed"
	"strings"
)

//go:embed data/apps.list
var list string

// App is one entry: an id, what it is called and shown with, and its rules in
// the rules language's kind:value, values unquoted.
type App struct {
	ID, Name, Icon, About string
	Rules                 []string
}

// Domains, Sets and Others split the rules as a group keeps them.
func (a App) Domains() []string { return a.of("domain") }
func (a App) Sets() []string    { return a.of("set") }

// Others are the rules that are neither domains nor sets, as kind:value.
func (a App) Others() []string {
	var out []string
	for _, r := range a.Rules {
		if k, _, _ := strings.Cut(r, ":"); k != "domain" && k != "set" {
			out = append(out, r)
		}
	}
	return out
}

func (a App) of(kind string) []string {
	var out []string
	for _, r := range a.Rules {
		if v, ok := strings.CutPrefix(r, kind+":"); ok {
			out = append(out, v)
		}
	}
	return out
}

// All lists the library, in its order.
func All() []App {
	var out []App
	for _, line := range strings.Split(list, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.SplitN(line, "|", 5)
		if len(f) != 5 {
			continue
		}
		for i := range f {
			f[i] = strings.TrimSpace(f[i])
		}
		out = append(out, App{ID: f[0], Name: f[1], Icon: f[2], About: f[3], Rules: Tokens(f[4])})
	}
	return out
}

// Find is the entry of that id.
func Find(id string) (App, bool) {
	for _, a := range All() {
		if a.ID == id {
			return a, true
		}
	}
	return App{}, false
}

// Search lists the entries whose id, name or rules hold q, ignoring case; all
// of them for an empty q.
func Search(q string) []App {
	q = strings.ToLower(strings.TrimSpace(q))
	var out []App
	for _, a := range All() {
		hay := strings.ToLower(a.ID + " " + a.Name + " " + strings.Join(a.Rules, " "))
		if q == "" || strings.Contains(hay, q) {
			out = append(out, a)
		}
	}
	return out
}

// Tokens splits kind:value tokens at spaces, except inside double quotes,
// which are dropped.
func Tokens(s string) []string {
	var out []string
	var cur strings.Builder
	in := false
	for _, r := range s {
		switch {
		case r == '"':
			in = !in
		case (r == ' ' || r == '\t' || r == '\n') && !in:
			if cur.Len() > 0 {
				out = append(out, cur.String())
				cur.Reset()
			}
		default:
			cur.WriteRune(r)
		}
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}
