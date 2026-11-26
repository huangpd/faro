package rules

import (
	"strings"
	"testing"
)

// An exit's name may be Chinese; what it may not hold is said, as it is typed.
func TestCheckRouteID(t *testing.T) {
	for _, ok := range []string{"work", "工作", "英国-办公", "日本2", "a-b-3", "claude", "办公-uk", strings.Repeat("工", 24)} {
		if msg := CheckRouteID(ok); msg != "" {
			t.Errorf("%q refused: %s", ok, msg)
		}
	}
	for id, want := range map[string]string{
		"":                      "请给出口起个名字",
		"my work":               "不能有空格",
		"工作\t2":                 "不能有空格",
		`a"b`:                   "不能有引号",
		"a“b”":                  "不能有引号",
		"a#b":                   "不能有 #",
		"a:b":                   "不能有冒号",
		"Work":                  "英文请用小写",
		"UK":                    "英文请用小写",
		"a_b":                   "不能有「_」",
		"a.b":                   "不能有「.」",
		"工作：家":                  "不能有「：」",
		"🇬🇧":                    "不能有「",
		"2work":                 "要以中文或字母开头",
		"-a":                    "要以中文或字母开头",
		strings.Repeat("工", 25): "最长 24 个字，现在是 25 个",
		"proxy":                 "是 Faro 自己用的词",
		"direct":                "是 Faro 自己用的词",
		"route":                 "是 Faro 自己用的词",
		"region":                "是 Faro 自己用的词",
		"notice":                "是 Faro 自己用的词",
	} {
		if msg := CheckRouteID(id); !strings.Contains(msg, want) {
			t.Errorf("%q: %q, want %q", id, msg, want)
		}
	}
}

// A Chinese exit is written, sent to and blocked like any other; the words a
// line starts with are refused as names, not taken as statements.
func TestAChineseExitCompiles(t *testing.T) {
	p, errs := Compile("route 工作 region UK gate noudp\n工作 domain:example.com\n", []string{"🇬🇧 London 01"}, nil)
	if errs != nil {
		t.Fatal(errs)
	}
	if len(p.Routes) != 1 || p.Routes[0].ID != "工作" || p.Routes[0].Group() != "route-工作" {
		t.Fatalf("routes %+v", p.Routes)
	}
	var to, guard bool
	for _, r := range p.Rules {
		to = to || r.Target == "route-工作"
		guard = guard || r.Guard == "工作"
	}
	if !to || !guard {
		t.Errorf("rules %+v", p.Rules)
	}
	for _, w := range []string{"notice", "region", "route"} {
		_, errs := Compile("route "+w+" region UK\n"+w+" domain:x.com\n", []string{"🇬🇧 London 01"}, nil)
		if len(errs) == 0 || !strings.Contains(errs[0].Msg, "Faro 自己用的词") || errs[0].Line != 1 {
			t.Errorf("%s: %v", w, errs)
		}
	}
}
