// Package guard holds tests about the code base itself.
package guard

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The program must work for anyone's rules: no service, company or product
// that one user happens to care about may be written into the code. What a
// user wants is theirs to write in the rules page; the one text Faro ships
// (internal/presets/data) is neutral.
//
// Tests may name them (internal/fixtures holds a setup for them); the program
// and what it ships may not.
var forbidden = regexp.MustCompile(`(?i)claude|anthropic|openai|chatgpt|gemini|antigravity|codex|trojanflare|google|youtube`)

func TestNoServiceNamesInCode(t *testing.T) {
	root, _ := filepath.Abs("../..")
	var offences []string
	filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		if info.IsDir() {
			switch rel {
			case ".git", "build", "resources", "design-system", "bin", ".agents", "internal/presets/data":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		for i, line := range strings.Split(string(b), "\n") {
			if m := forbidden.FindString(line); m != "" {
				offences = append(offences, rel+":"+itoa(i+1)+": "+strings.TrimSpace(line))
			}
		}
		return nil
	})
	if len(offences) > 0 {
		t.Errorf("service names in the code (rules are the person's own to write):\n  %s", strings.Join(offences, "\n  "))
	}
}

func itoa(n int) string {
	s := ""
	for n > 0 {
		s = string(rune('0'+n%10)) + s
		n /= 10
	}
	if s == "" {
		return "0"
	}
	return s
}

// The rules a new install starts with name no one's service: a setup of one
// person's accounts must not ship as everyone's default.
func TestShippedRulesNameNoService(t *testing.T) {
	files, _ := filepath.Glob("../presets/data/*.rules")
	if len(files) == 0 {
		t.Fatal("no shipped rules found")
	}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if m := forbidden.FindString(string(b)); m != "" {
			t.Errorf("%s names a service (%q): it ships to everyone", f, m)
		}
	}
	if len(files) != 1 {
		t.Errorf("%d shipped rule files %v; a new install needs only the base", len(files), files)
	}
}
