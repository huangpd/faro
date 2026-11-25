package rules

import (
	"net/netip"
	"strings"
)

// The rules are checked top to bottom, and the first that matches decides. So
// a value written in a rule can be out of reach: a rule above it, going
// elsewhere, already takes everything it would. Shadows finds those.

// Shadow is one value of a rule that never decides: the rule at By (an index
// into the same list) takes it first, and sends it somewhere else.
type Shadow struct {
	Value string
	By    int
}

// addressKinds are the conditions sing-box joins with "or" inside one rule;
// every other kind is joined to them with "and".
var addressKinds = map[string]bool{
	"domain": true, "domain_suffix": true, "domain_keyword": true, "domain_regex": true,
	"ip_cidr": true, "rule_set": true, "rule_set_ip": true,
}

// Shadows says, for each rule, which of its values are taken by an earlier
// rule with another target. It is sure of what it reports, and says nothing
// where it cannot be: a rule set is compared by name only, not by the sites in
// it, and a rule above with conditions joined by "and" takes only some of a
// value's traffic, so it is not counted.
func Shadows(rs []Rule) [][]Shadow {
	out := make([][]Shadow, len(rs))
	for j, later := range rs {
		for _, m := range later.Matchers {
			for _, v := range m.Values {
				for i := 0; i < j; i++ {
					if rs[i].Target != later.Target && takesAll(rs[i], later, m.Kind, v) {
						out[j] = append(out[j], Shadow{v, i})
						break
					}
				}
			}
		}
	}
	return out
}

// takesAll says whether every connection that matches kind:v in later, with
// later's network and IP version, is matched by earlier.
func takesAll(earlier, later Rule, kind, v string) bool {
	if earlier.Network != "" && earlier.Network != later.Network {
		return false
	}
	if earlier.IPVersion != 0 && earlier.IPVersion != later.IPVersion {
		return false
	}
	// earlier's conditions must be one "or": all of address kinds, or one kind.
	if len(earlier.Matchers) > 1 {
		for _, m := range earlier.Matchers {
			if !addressKinds[m.Kind] {
				return false
			}
		}
	}
	for _, m := range earlier.Matchers {
		for _, e := range m.Values {
			if covers(m.Kind, e, kind, v) {
				return true
			}
		}
	}
	return false
}

// covers says whether the condition ak:a matches everything bk:b matches.
func covers(ak, a, bk, b string) bool {
	switch ak {
	case "domain_suffix":
		return (bk == "domain_suffix" || bk == "domain") && underDomain(b, a)
	case "domain":
		return bk == "domain" && strings.EqualFold(a, b)
	case "domain_keyword":
		return (bk == "domain" || bk == "domain_suffix" || bk == "domain_keyword") &&
			strings.Contains(strings.ToLower(b), strings.ToLower(a))
	case "ip_cidr":
		if bk != "ip_cidr" {
			return false
		}
		pa, ok1 := prefix(a)
		pb, ok2 := prefix(b)
		return ok1 && ok2 && pa.Bits() <= pb.Bits() && pa.Contains(pb.Addr())
	case "domain_regex", "process_path_regex", "rule_set", "rule_set_ip", "process_name", "port":
		return ak == bk && a == b
	}
	return false
}

// underDomain says whether name is domain or a name under it.
func underDomain(name, domain string) bool {
	name, domain = strings.ToLower(strings.Trim(name, ".")), strings.ToLower(strings.Trim(domain, "."))
	return name == domain || strings.HasSuffix(name, "."+domain)
}

// prefix reads an address range, or a single address as a range of one.
func prefix(s string) (netip.Prefix, bool) {
	if p, err := netip.ParsePrefix(s); err == nil {
		return p.Masked(), true
	}
	if a, err := netip.ParseAddr(s); err == nil {
		return netip.PrefixFrom(a, a.BitLen()), true
	}
	return netip.Prefix{}, false
}
