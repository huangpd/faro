package ruleset

import "testing"

func content(t *testing.T, js string) *Content {
	t.Helper()
	c, err := ParseContent([]byte(js))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestContentMatchesNamesTheWayTheCoreDoes(t *testing.T) {
	c := content(t, `{"version":1,"rules":[{"domain":["Exact.Example.org"],"domain_suffix":["example.com",".sub.test"],"domain_keyword":["kw"],"domain_regex":["^r[0-9]+\\.x$","("]}]}`)
	for host, want := range map[string]bool{
		"example.com": true, "www.example.com": true, "a.b.example.com": true, "EXAMPLE.com.": true,
		"notexample.com": false, "exact.example.org": true, "x.exact.example.org": false,
		"sub.test": true, "a.sub.test": true, "mykwhost.net": true, "r12.x": true, "r.x": false, "other.net": false,
	} {
		if c.Match(host) != want {
			t.Errorf("Match(%q) = %v, want %v", host, !want, want)
		}
	}
}

func TestReachesSeesWhatLiesUnderAName(t *testing.T) {
	c := content(t, `{"version":1,"rules":[{"domain_suffix":["mail.example.com"],"domain":["x.shop.net"]}]}`)
	for name, want := range map[string]bool{
		"example.com": true, "mail.example.com": true, "a.mail.example.com": true, "shop.net": true,
		"other.com": false, "ample.com": false,
	} {
		if c.Reaches(name) != want {
			t.Errorf("Reaches(%q) want %v", name, want)
		}
	}
}

func TestOverlapCountsNamesDecidedByBoth(t *testing.T) {
	a := content(t, `{"version":1,"rules":[{"domain_suffix":["a.com","b.com","video.net"]}]}`)
	b := content(t, `{"version":1,"rules":[{"domain_suffix":["b.com","c.com"],"domain":["www.a.com"]}]}`)
	n, ex := Overlap(a, b)
	if n < 2 || ex == "" {
		t.Errorf("overlap %d %q", n, ex)
	}
	c := content(t, `{"version":1,"rules":[{"domain_suffix":["zzz.org"]}]}`)
	if n, _ := Overlap(a, c); n != 0 {
		t.Errorf("sets with nothing in common overlap %d", n)
	}
}
