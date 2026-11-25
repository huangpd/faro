package main

import (
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

// rulesLanguage tells the code editor how the rules text reads: what is a
// statement's word, what a flag, what a kind and its value, what a comment.
// It knows the shape of the language, not what anyone writes in it.
var rulesLanguage = &editor.Language{
	Name:        "faro-rules",
	LineComment: "#",
	Tokenizer:   editor.TokenizerFunc(tokenizeRules),
	AutoClose:   []editor.BracketPair{{Open: `"`, Close: `"`}},
}

var (
	ruleStatements = map[string]bool{"route": true, "region": true, "notice": true}
	ruleHow        = map[string]bool{"region": true, "regex": true}
	ruleFlags      = map[string]bool{"udp": true, "tcp": true, "ipv4": true, "ipv6": true, "fast": true, "gate": true}
)

// tokenizeRules splits one line into the runs the editor colors. A line is
// read alone, so the state is always 0.
func tokenizeRules(line string, _ uint64, out []editor.Token) ([]editor.Token, uint64) {
	add := func(end int, k editor.TokenKind) {
		if n := len(out); n > 0 && out[n-1].Kind == k {
			out[n-1].End = end
			return
		}
		out = append(out, editor.Token{End: end, Kind: k})
	}
	i, word := 0, 0
	var statement string
	for i < len(line) {
		if line[i] == ' ' || line[i] == '\t' {
			j := i
			for j < len(line) && (line[j] == ' ' || line[j] == '\t') {
				j++
			}
			add(j, editor.TokenText)
			i = j
			continue
		}
		if line[i] == '#' { // a comment starts a word, never ends one
			add(len(line), editor.TokenComment)
			break
		}
		// The word runs to a space outside quotes.
		j, in := i, false
		for j < len(line) {
			if line[j] == '"' {
				in = !in
			} else if !in && (line[j] == ' ' || line[j] == '\t') {
				break
			}
			j++
		}
		w := line[i:j]
		switch {
		case word == 0 && ruleStatements[w]:
			statement = w
			add(j, editor.TokenKeyword)
		case word == 0: // not a statement: a target
			add(j, editor.TokenFunction)
		case statement == "route" && word == 1:
			add(j, editor.TokenVariable)
		case statement == "route" && word == 2 && ruleHow[w]:
			add(j, editor.TokenKeyword)
		case ruleFlags[w]:
			add(j, editor.TokenConstant)
		case strings.HasPrefix(w, `"`):
			add(j, editor.TokenString)
		default:
			if k := strings.IndexByte(w, ':'); k > 0 && isWord(w[:k]) {
				add(i+k, editor.TokenProperty)
				add(i+k+1, editor.TokenPunctuation)
				add(j, editor.TokenString)
			} else {
				add(j, editor.TokenText)
			}
		}
		i = j
		word++
	}
	return out, 0
}

func isWord(s string) bool {
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r == '-' || r == '_') {
			return false
		}
	}
	return s != ""
}

// rulesEditor is the rules text in a code editor: line numbers, colors, and
// the mistakes marked on their lines. a.rulesEdit stays the text; this keeps
// the buffer and it the same, whichever was changed last.
func (a *app) rulesEditor(c *ui.Context, open bool) {
	if a.rulesCode == nil {
		buf := editor.NewBuffer(a.rulesEdit)
		buf.SetLanguage(rulesLanguage)
		a.rulesCode = &editor.State{Buffer: buf, Label: "规则文本", NoFolding: true, NoIndentGuides: true, SoftWrap: true}
		a.rulesSeen, a.rulesVer = a.rulesEdit, buf.Version()
	}
	buf := a.rulesCode.Buffer
	if a.rulesEdit != a.rulesSeen { // set from outside: a reset, or a test
		buf.Set(a.rulesEdit)
		a.rulesSeen, a.rulesVer = a.rulesEdit, buf.Version()
	} else if v := buf.Version(); v != a.rulesVer { // typed
		a.rulesEdit = buf.String()
		a.rulesSeen, a.rulesVer = a.rulesEdit, v
	}
	a.rulesCode.ReadOnly = !open
	a.markRulesErrors()
	ui.Column(c).Grow(1).Radius(12).Border(1, a.k.border).Clip().Children(func() {
		editor.View(c, a.rulesCode).Grow(1)
	})
}

// markRulesErrors puts each mistake on its line, when they or the text change.
func (a *app) markRulesErrors() {
	key := a.rulesVer
	if a.rulesMarked == key && a.rulesMarkedErrs == len(a.rulesErrs) {
		return
	}
	a.rulesMarked, a.rulesMarkedErrs = key, len(a.rulesErrs)
	lines := strings.Split(a.rulesEdit, "\n")
	var ds []editor.Diagnostic
	for _, e := range a.rulesErrs {
		start := 0
		for n := 0; n < e.Line-1 && n < len(lines); n++ {
			start += len(lines[n]) + 1
		}
		end := start
		if e.Line >= 1 && e.Line <= len(lines) {
			end = start + len(lines[e.Line-1])
		}
		ds = append(ds, editor.Diagnostic{Start: start, End: end, Severity: editor.SeverityError, Message: e.Msg})
	}
	a.rulesCode.SetDiagnostics(a.rulesVer, ds)
}
