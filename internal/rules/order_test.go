package rules

import "testing"

func TestShadows(t *testing.T) {
	p, errs := Compile(`route uk region UK
route de region DE
proxy domain:youtube.com domain:ytimg.com
de domain:google.com domain:ytimg.com domain:i.ytimg.com full:www.youtube.com
block udp domain:claude.ai
uk domain:claude.ai
uk keyword:datadog
de full:browser-intake-datadoghq.com
direct cidr:10.0.0.0/8
uk cidr:10.1.2.3 cidr:11.0.0.1
proxy port:443 process:chrome
de domain:x.chrome.com process:chrome
proxy domain:google.com
de domain:mail.google.com
`, []string{"🇬🇧 UK 1", "🇩🇪 DE 1"}, nil)
	if errs != nil {
		t.Fatal(errs)
	}
	sh := Shadows(p.Rules)
	got := map[string]int{} // value -> the index of the rule that takes it
	for _, s := range sh {
		for _, x := range s {
			got[x.Value] = x.By
		}
	}
	want := map[string]int{
		"ytimg.com": 0, "i.ytimg.com": 0, "www.youtube.com": 0, // under proxy's domains
		"browser-intake-datadoghq.com": 4,  // the keyword above
		"10.1.2.3":                     6,  // inside 10.0.0.0/8
		"mail.google.com":              10, // under proxy's google.com, which is above it
	}
	for v, by := range want {
		if g, ok := got[v]; !ok || g != by {
			t.Errorf("%s: taken by %d (%v), want %d", v, g, ok, by)
		}
	}
	for _, v := range []string{
		"claude.ai",    // the block above is UDP only: TCP still reaches the route
		"11.0.0.1",     // outside the range
		"x.chrome.com", // the rule above wants port 443 AND chrome: it takes only some
		"chrome",       // likewise
	} {
		if by, ok := got[v]; ok {
			t.Errorf("%s reported as taken by %d", v, by)
		}
	}
	// proxy's google.com comes after de's: it is out of reach.
	if len(sh[10]) != 1 || sh[10][0] != (Shadow{"google.com", 1}) {
		t.Errorf("proxy's google.com, below de's: %v", sh[10])
	}
}
