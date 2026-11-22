// Package sub turns a subscription into sing-box outbounds. A provider may
// serve share links (plain or base64), Clash YAML or sing-box JSON; all
// become Nodes, whose Out is a sing-box outbound without its tag.
package sub

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Node is one proxy server.
type Node struct {
	Name string         `json:"name"`
	Out  map[string]any `json:"out"`
}

// Parse detects the format of a subscription body.
func Parse(body []byte) ([]Node, error) {
	s := strings.TrimSpace(strings.TrimPrefix(string(body), "\uFEFF"))
	if s == "" {
		return nil, errors.New("empty subscription")
	}
	var nodes []Node
	switch {
	case strings.HasPrefix(s, "{"):
		nodes = fromSingBox(s)
	case strings.Contains(s, "proxies:"):
		nodes = fromClash(s)
	default:
		if dec, ok := decodeB64(s); ok && strings.Contains(dec, "://") {
			s = dec
		}
		nodes = fromLinks(s)
	}
	if len(nodes) == 0 {
		return nil, errors.New("no supported nodes found")
	}
	return dropNotices(nodes)
}

// dropNotices removes the entries a provider puts in place of servers: a
// node that points at this very machine is a message ("access restricted",
// "plan expired"), never a server. When nothing else is left, the message
// is the error, so that it is read and not imported as a node.
func dropNotices(nodes []Node) ([]Node, error) {
	var real []Node
	var notice string
	for _, n := range nodes {
		if isLocalServer(n.Out["server"]) {
			if notice == "" {
				notice = n.Name
			}
			continue
		}
		real = append(real, n)
	}
	if len(real) == 0 {
		return nil, fmt.Errorf("the provider sent a notice instead of nodes: %q", notice)
	}
	return real, nil
}

func isLocalServer(v any) bool {
	host, _ := v.(string)
	host = strings.ToLower(strings.Trim(host, "[]"))
	if host == "localhost" || host == "0.0.0.0" || host == "::" || host == "::1" || host == "" {
		return true
	}
	return strings.HasPrefix(host, "127.")
}

func decodeB64(s string) (string, bool) {
	s = strings.Join(strings.Fields(s), "")
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if b, err := enc.DecodeString(s); err == nil {
			return string(b), true
		}
	}
	return "", false
}

// ---- sing-box JSON

func fromSingBox(s string) []Node {
	var cfg struct {
		Outbounds []map[string]any `json:"outbounds"`
	}
	if json.Unmarshal([]byte(s), &cfg) != nil {
		return nil
	}
	skip := map[string]bool{"direct": true, "block": true, "dns": true, "selector": true, "urltest": true}
	var out []Node
	for _, o := range cfg.Outbounds {
		typ, _ := o["type"].(string)
		tag, _ := o["tag"].(string)
		if typ == "" || skip[typ] || tag == "" {
			continue
		}
		delete(o, "tag")
		out = append(out, Node{Name: tag, Out: o})
	}
	return out
}

// ---- share links

func fromLinks(s string) []Node {
	var out []Node
	// One link per line: names may hold raw spaces.
	for _, line := range strings.FieldsFunc(s, func(r rune) bool { return r == '\n' || r == '\r' }) {
		if n, err := ParseLink(strings.TrimSpace(line)); err == nil {
			out = append(out, n)
		}
	}
	return out
}

// ParseLink reads one share link.
func ParseLink(link string) (Node, error) {
	scheme, _, ok := strings.Cut(link, "://")
	if !ok {
		return Node{}, errors.New("not a link")
	}
	switch strings.ToLower(scheme) {
	case "vmess":
		return parseVmess(link)
	case "ss":
		return parseSS(link)
	case "vless", "trojan", "hysteria2", "hy2", "tuic":
		return parseURLLink(link)
	}
	return Node{}, fmt.Errorf("unsupported scheme %q", scheme)
}

func atoi(s string) int { n, _ := strconv.Atoi(s); return n }

func parseVmess(link string) (Node, error) {
	raw, ok := decodeB64(strings.TrimPrefix(link, "vmess://"))
	if !ok {
		return Node{}, errors.New("bad vmess")
	}
	var v map[string]any
	if json.Unmarshal([]byte(raw), &v) != nil {
		return Node{}, errors.New("bad vmess json")
	}
	str := func(k string) string {
		switch x := v[k].(type) {
		case string:
			return x
		case float64:
			return strconv.FormatFloat(x, 'f', -1, 64)
		}
		return ""
	}
	out := map[string]any{
		"type": "vmess", "server": str("add"), "server_port": atoi(str("port")),
		"uuid": str("id"), "security": orDefault(str("scy"), "auto"), "alter_id": atoi(str("aid")),
	}
	if str("tls") == "tls" {
		out["tls"] = tlsOpts(orDefault(str("sni"), str("host")), false, str("fp"), str("alpn"), nil)
	}
	setTransport(out, str("net"), str("path"), str("host"), str("path"))
	return finish(out, str("ps"))
}

func parseSS(link string) (Node, error) {
	rest := strings.TrimPrefix(link, "ss://")
	rest, frag, _ := strings.Cut(rest, "#")
	rest, _, _ = strings.Cut(rest, "?") // plugins are not supported
	var userinfo, hostport string
	if at := strings.LastIndex(rest, "@"); at >= 0 {
		userinfo, hostport = rest[:at], rest[at+1:]
		if dec, ok := decodeB64(userinfo); ok && strings.Contains(dec, ":") {
			userinfo = dec
		}
	} else if dec, ok := decodeB64(rest); ok {
		at := strings.LastIndex(dec, "@")
		if at < 0 {
			return Node{}, errors.New("bad ss")
		}
		userinfo, hostport = dec[:at], dec[at+1:]
	}
	method, pass, ok := strings.Cut(userinfo, ":")
	host, port, ok2 := splitHostPort(hostport)
	if !ok || !ok2 {
		return Node{}, errors.New("bad ss")
	}
	name, _ := url.PathUnescape(frag)
	return finish(map[string]any{"type": "shadowsocks", "server": host, "server_port": port, "method": method, "password": pass}, name)
}

func splitHostPort(s string) (string, int, bool) {
	s = strings.TrimSuffix(s, "/")
	i := strings.LastIndex(s, ":")
	if i < 0 {
		return "", 0, false
	}
	p, err := strconv.Atoi(s[i+1:])
	return strings.Trim(s[:i], "[]"), p, err == nil && p > 0
}

func parseURLLink(link string) (Node, error) {
	u, err := url.Parse(link)
	if err != nil || u.Hostname() == "" {
		return Node{}, errors.New("bad link")
	}
	q := u.Query()
	port := atoi(u.Port())
	secret := u.User.Username()
	pass, hasPass := u.User.Password()
	out := map[string]any{"server": u.Hostname(), "server_port": port}
	insecure := q.Get("insecure") == "1" || q.Get("allowInsecure") == "1" || q.Get("allow_insecure") == "1"
	sni := orDefault(q.Get("sni"), q.Get("peer"))

	switch strings.ToLower(u.Scheme) {
	case "vless":
		out["type"], out["uuid"] = "vless", secret
		if f := q.Get("flow"); f != "" {
			out["flow"] = f
		}
		switch q.Get("security") {
		case "tls", "reality":
			t := tlsOpts(orDefault(sni, q.Get("host")), insecure, q.Get("fp"), q.Get("alpn"), nil)
			if q.Get("security") == "reality" {
				t["reality"] = map[string]any{"enabled": true, "public_key": q.Get("pbk"), "short_id": q.Get("sid")}
				if _, ok := t["utls"]; !ok {
					t["utls"] = map[string]any{"enabled": true, "fingerprint": "chrome"}
				}
			}
			out["tls"] = t
		}
		setTransport(out, q.Get("type"), q.Get("path"), q.Get("host"), q.Get("serviceName"))
	case "trojan":
		out["type"], out["password"] = "trojan", secret
		out["tls"] = tlsOpts(orDefault(sni, u.Hostname()), insecure, q.Get("fp"), q.Get("alpn"), nil)
		setTransport(out, q.Get("type"), q.Get("path"), q.Get("host"), q.Get("serviceName"))
	case "hysteria2", "hy2":
		out["type"] = "hysteria2"
		if hasPass {
			secret += ":" + pass
		}
		out["password"] = secret
		out["tls"] = tlsOpts(orDefault(sni, u.Hostname()), insecure, "", q.Get("alpn"), nil)
		if o := q.Get("obfs"); o != "" {
			out["obfs"] = map[string]any{"type": o, "password": q.Get("obfs-password")}
		}
	case "tuic":
		out["type"], out["uuid"], out["password"] = "tuic", secret, pass
		out["tls"] = tlsOpts(orDefault(sni, u.Hostname()), insecure, "", q.Get("alpn"), nil)
		if v := q.Get("congestion_control"); v != "" {
			out["congestion_control"] = v
		}
		if v := q.Get("udp_relay_mode"); v != "" {
			out["udp_relay_mode"] = v
		}
	}
	return finish(out, u.Fragment)
}

// ---- Clash YAML

func fromClash(s string) []Node {
	var doc struct {
		Proxies []map[string]any `yaml:"proxies"`
	}
	if yaml.Unmarshal([]byte(s), &doc) != nil {
		return nil
	}
	var out []Node
	for _, p := range doc.Proxies {
		if n, err := clashNode(p); err == nil {
			out = append(out, n)
		}
	}
	return out
}

func clashNode(p map[string]any) (Node, error) {
	str := func(k string) string {
		switch x := p[k].(type) {
		case string:
			return x
		case int:
			return strconv.Itoa(x)
		case bool:
			return strconv.FormatBool(x)
		}
		return ""
	}
	sub := func(k string) map[string]any { m, _ := p[k].(map[string]any); return m }
	subStr := func(m map[string]any, k string) string { s, _ := m[k].(string); return s }
	tlsOn := p["tls"] == true
	sni := orDefault(str("servername"), str("sni"))
	insecure := p["skip-cert-verify"] == true
	var alpn string
	if a, ok := p["alpn"].([]any); ok {
		var parts []string
		for _, x := range a {
			parts = append(parts, fmt.Sprint(x))
		}
		alpn = strings.Join(parts, ",")
	}
	out := map[string]any{"server": str("server"), "server_port": atoi(str("port"))}
	transport := func() {
		net := str("network")
		switch net {
		case "ws":
			o := sub("ws-opts")
			host := ""
			if h, ok := o["headers"].(map[string]any); ok {
				host, _ = h["Host"].(string)
			}
			setTransport(out, "ws", subStr(o, "path"), host, "")
		case "grpc":
			setTransport(out, "grpc", "", "", subStr(sub("grpc-opts"), "grpc-service-name"))
		case "h2", "http":
			setTransport(out, "h2", subStr(sub("h2-opts"), "path"), "", "")
		}
	}
	switch str("type") {
	case "ss":
		out["type"], out["method"], out["password"] = "shadowsocks", str("cipher"), str("password")
	case "vmess":
		out["type"], out["uuid"], out["alter_id"] = "vmess", str("uuid"), atoi(str("alterId"))
		out["security"] = orDefault(str("cipher"), "auto")
		if tlsOn {
			out["tls"] = tlsOpts(orDefault(sni, str("server")), insecure, str("client-fingerprint"), alpn, nil)
		}
		transport()
	case "vless":
		out["type"], out["uuid"] = "vless", str("uuid")
		if f := str("flow"); f != "" {
			out["flow"] = f
		}
		if tlsOn || sub("reality-opts") != nil {
			t := tlsOpts(orDefault(sni, str("server")), insecure, str("client-fingerprint"), alpn, nil)
			if r := sub("reality-opts"); r != nil {
				t["reality"] = map[string]any{"enabled": true, "public_key": subStr(r, "public-key"), "short_id": subStr(r, "short-id")}
				if _, ok := t["utls"]; !ok {
					t["utls"] = map[string]any{"enabled": true, "fingerprint": "chrome"}
				}
			}
			out["tls"] = t
		}
		transport()
	case "trojan":
		out["type"], out["password"] = "trojan", str("password")
		out["tls"] = tlsOpts(orDefault(sni, str("server")), insecure, str("client-fingerprint"), alpn, nil)
		transport()
	case "hysteria2":
		out["type"], out["password"] = "hysteria2", str("password")
		out["tls"] = tlsOpts(orDefault(sni, str("server")), insecure, "", alpn, nil)
		if o := str("obfs"); o != "" {
			out["obfs"] = map[string]any{"type": o, "password": str("obfs-password")}
		}
	case "tuic":
		out["type"], out["uuid"], out["password"] = "tuic", str("uuid"), str("password")
		out["tls"] = tlsOpts(orDefault(sni, str("server")), insecure, "", alpn, nil)
		if v := str("congestion-controller"); v != "" {
			out["congestion_control"] = v
		}
		if v := str("udp-relay-mode"); v != "" {
			out["udp_relay_mode"] = v
		}
	default:
		return Node{}, fmt.Errorf("unsupported type %q", str("type"))
	}
	return finish(out, str("name"))
}

// ---- shared pieces

func orDefault(s, d string) string {
	if s != "" {
		return s
	}
	return d
}

func tlsOpts(sni string, insecure bool, fp, alpn string, _ any) map[string]any {
	t := map[string]any{"enabled": true}
	if sni != "" {
		t["server_name"] = sni
	}
	if insecure {
		t["insecure"] = true
	}
	if fp != "" {
		t["utls"] = map[string]any{"enabled": true, "fingerprint": fp}
	}
	if alpn != "" {
		t["alpn"] = strings.Split(alpn, ",")
	}
	return t
}

// setTransport sets a sing-box transport. Only ws, grpc and h2 are mapped;
// plain tcp has none. svc is the gRPC service name.
func setTransport(out map[string]any, net, path, host, svc string) {
	switch net {
	case "ws":
		tr := map[string]any{"type": "ws"}
		if path != "" {
			tr["path"] = path
		}
		if host != "" {
			tr["headers"] = map[string]any{"Host": host}
		}
		out["transport"] = tr
	case "grpc":
		out["transport"] = map[string]any{"type": "grpc", "service_name": svc}
	case "h2", "http":
		tr := map[string]any{"type": "http"}
		if path != "" {
			tr["path"] = path
		}
		if host != "" {
			tr["host"] = strings.Split(host, ",")
		}
		out["transport"] = tr
	}
}

func finish(out map[string]any, name string) (Node, error) {
	if out["server"] == "" || out["server_port"] == 0 {
		return Node{}, errors.New("missing server")
	}
	if name == "" {
		name = fmt.Sprintf("%v:%v", out["server"], out["server_port"])
	}
	return Node{Name: name, Out: out}, nil
}

// Describe says what a subscription body is, and what in it Parse leaves
// out: the format, and how many entries of each unsupported kind. It never
// returns anything of a node but what Parse itself would keep.
func Describe(body []byte) (format string, skipped map[string]int) {
	skipped = map[string]int{}
	s := strings.TrimSpace(strings.TrimPrefix(string(body), "\uFEFF"))
	switch {
	case strings.HasPrefix(s, "{"):
		return "sing-box JSON", skipped
	case strings.Contains(s, "proxies:"):
		var doc struct {
			Proxies []map[string]any `yaml:"proxies"`
		}
		if yaml.Unmarshal([]byte(s), &doc) == nil {
			for _, p := range doc.Proxies {
				if _, err := clashNode(p); err != nil {
					t, _ := p["type"].(string)
					skipped["clash:"+t]++
				}
			}
		}
		return "Clash YAML", skipped
	}
	format = "share links"
	if dec, ok := decodeB64(s); ok && strings.Contains(dec, "://") {
		s, format = dec, "share links (base64)"
	}
	for _, line := range strings.FieldsFunc(s, func(r rune) bool { return r == '\n' || r == '\r' }) {
		line = strings.TrimSpace(line)
		if _, err := ParseLink(line); err != nil {
			scheme, _, _ := strings.Cut(line, "://")
			if len(scheme) > 12 || scheme == line {
				scheme = "other"
			}
			skipped[scheme]++
		}
	}
	return format, skipped
}
