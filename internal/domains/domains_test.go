package domains

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestOneReadsASiteOutOfWhatWasPasted(t *testing.T) {
	for in, want := range map[string]string{
		"youtube.com":                         "youtube.com",
		"  YouTube.COM ":                      "youtube.com",
		"https://www.youtube.com/watch?v=abc": "www.youtube.com",
		"http://user:pw@example.org:8080/x#y": "example.org",
		"*.example.com":                       "example.com",
		".example.com.":                       "example.com",
		"a-b.c1.example.co.uk":                "a-b.c1.example.co.uk",
	} {
		if got, ok := One(in); !ok || got != want {
			t.Errorf("One(%q) = %q %v, want %q", in, got, ok, want)
		}
	}
	for _, in := range []string{"", "localhost", "192.168.1.1", "1.2.3.4:80", "exa mple.com", "-a.com", "a..com", "例子.中国", "https://", "a.b_c.com"} {
		if got, ok := One(in); ok {
			t.Errorf("One(%q) = %q, should not be a site", in, got)
		}
	}
}

func TestParseTakesManyAndKeepsTheOrderWithoutRepeats(t *testing.T) {
	ok, bad := Parse("a.com, b.com\nA.com https://c.com/x  nope ;d.org，e.org")
	if !reflect.DeepEqual(ok, []string{"a.com", "b.com", "c.com", "d.org", "e.org"}) || !reflect.DeepEqual(bad, []string{"nope"}) {
		t.Errorf("ok %v bad %v", ok, bad)
	}
}

func TestBaseOfAHost(t *testing.T) {
	for in, want := range map[string]string{
		"www.youtube.com":         "youtube.com",
		"r3---sn.googlevideo.com": "googlevideo.com",
		"example.com":             "example.com",
		"www.bbc.co.uk":           "bbc.co.uk",
		"news.sina.com.cn":        "sina.com.cn",
		"a.b.example.cn":          "example.cn",
		"1.2.3.4":                 "1.2.3.4",
	} {
		if got := Base(in); got != want {
			t.Errorf("Base(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestHeadMakesRulesInOrderAndSaysWhoseLineIsWhose(t *testing.T) {
	var many []string
	for i := 0; i < 10; i++ {
		many = append(many, "d"+string(rune('a'+i))+".com")
	}
	head, owners := Head([]Group{
		{Name: "one", Target: "direct", Domains: []string{"a.com", "b.com"}},
		{Name: "empty", Target: "proxy"},
		{Name: "two", Target: "claude", Domains: many},
	})
	lines := strings.Split(strings.TrimRight(head, "\n"), "\n")
	if len(lines) != 3 || lines[0] != "direct domain:a.com domain:b.com" ||
		!strings.HasPrefix(lines[1], "claude domain:da.com") || !strings.HasPrefix(lines[2], "claude domain:di.com") {
		t.Fatalf("head %q", head)
	}
	if !reflect.DeepEqual(owners, []int{0, 2, 2}) {
		t.Errorf("owners %v", owners)
	}
	if h, o := Head(nil); h != "" || o != nil {
		t.Error("no groups, no rules")
	}
}

func TestSaveAndLoad(t *testing.T) {
	p := filepath.Join(t.TempDir(), "domains.json")
	if g, err := Load(p); err != nil || g != nil {
		t.Fatalf("a missing file is no groups: %v %v", g, err)
	}
	in := []Group{{Name: "AI", Target: "claude", Domains: []string{"a.com"}, Sets: []string{"geosite-x"}}, {Name: "直连", Target: "direct"}}
	if err := Save(p, in); err != nil {
		t.Fatal(err)
	}
	out, err := Load(p)
	if err != nil || !reflect.DeepEqual(out, in) {
		t.Errorf("%v %v", out, err)
	}
	c := Copy(out)
	c[0].Domains[0] = "changed.com"
	if out[0].Domains[0] != "a.com" {
		t.Error("the copy shares its sites")
	}
}

func TestHeadWritesSetsAfterTheSitesOfTheirGroup(t *testing.T) {
	head, owners := Head([]Group{
		{Name: "g", Target: "claude", Domains: []string{"a.com"}, Sets: []string{"geosite-x", "geosite-y@cn"}},
		{Name: "h", Target: "direct", Sets: []string{"geosite-z"}},
	})
	want := "claude domain:a.com\nclaude set:geosite-x set:geosite-y@cn\ndirect set:geosite-z\n"
	if head != want || !reflect.DeepEqual(owners, []int{0, 0, 1}) {
		t.Errorf("head %q owners %v", head, owners)
	}
	c := Copy([]Group{{Name: "g", Sets: []string{"geosite-x"}}})
	c[0].Sets[0] = "changed"
	if Copy(c)[0].Sets[0] != "changed" {
		t.Error("copy keeps sets")
	}
}

// A group's other rules go on lines of their own kind, quoted where they have spaces.
func TestAGroupsOtherRulesAreLinesOfOneKind(t *testing.T) {
	head, owners := Head([]Group{{Name: "a", Target: "uk", Domains: []string{"a.example"},
		Rules: []string{"process:App", "full:api.example", "process:App Helper (GPU)", "cidr:203.0.113.0/24"}}})
	want := "uk domain:a.example\nuk process:App process:\"App Helper (GPU)\"\nuk full:api.example\nuk cidr:203.0.113.0/24\n"
	if head != want || len(owners) != 4 {
		t.Errorf("got\n%s%v", head, owners)
	}
	c := Copy([]Group{{Name: "a", Rules: []string{"process:x"}, App: "lib"}})
	if len(c[0].Rules) != 1 || c[0].App != "lib" {
		t.Errorf("copy lost them: %+v", c)
	}
}
