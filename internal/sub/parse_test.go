package sub

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

func b64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

func TestLinks(t *testing.T) {
	vmess, _ := json.Marshal(map[string]any{"ps": "vm-ws", "add": "a.example.com", "port": "443", "id": "uuid-1", "aid": "0",
		"net": "ws", "host": "cdn.example.com", "path": "/ws", "tls": "tls", "sni": "s.example.com"})
	cases := []struct {
		link, name, typ string
		check           func(t *testing.T, o map[string]any)
	}{
		{"vmess://" + b64(string(vmess)), "vm-ws", "vmess", func(t *testing.T, o map[string]any) {
			tr := o["transport"].(map[string]any)
			if tr["type"] != "ws" || tr["path"] != "/ws" || o["tls"].(map[string]any)["server_name"] != "s.example.com" {
				t.Errorf("vmess: %v", o)
			}
		}},
		{"vless://uuid-2@h.example.com:443?security=reality&pbk=PUB&sid=ab&sni=www.apple.com&fp=chrome&flow=xtls-rprx-vision#reality%20node", "reality node", "vless", func(t *testing.T, o map[string]any) {
			r := o["tls"].(map[string]any)["reality"].(map[string]any)
			if r["public_key"] != "PUB" || r["short_id"] != "ab" || o["flow"] != "xtls-rprx-vision" {
				t.Errorf("vless reality: %v", o)
			}
		}},
		{"trojan://pw@t.example.com:443?sni=t.example.com&type=grpc&serviceName=svc#tj", "tj", "trojan", func(t *testing.T, o map[string]any) {
			if o["transport"].(map[string]any)["service_name"] != "svc" || o["password"] != "pw" {
				t.Errorf("trojan: %v", o)
			}
		}},
		{"ss://" + b64("aes-256-gcm:secret") + "@s.example.com:8388#ss1", "ss1", "shadowsocks", func(t *testing.T, o map[string]any) {
			if o["method"] != "aes-256-gcm" || o["password"] != "secret" || o["server_port"] != 8388 {
				t.Errorf("ss: %v", o)
			}
		}},
		{"ss://" + b64("chacha20-ietf-poly1305:p@s2.example.com:1234") + "#legacy", "legacy", "shadowsocks", nil},
		{"hysteria2://hp@hy.example.com:8443?insecure=1&obfs=salamander&obfs-password=op&sni=x.com#hy", "hy", "hysteria2", func(t *testing.T, o map[string]any) {
			if o["obfs"].(map[string]any)["password"] != "op" || o["tls"].(map[string]any)["insecure"] != true {
				t.Errorf("hy2: %v", o)
			}
		}},
		{"tuic://u1:p1@tu.example.com:443?congestion_control=bbr&alpn=h3#tu", "tu", "tuic", nil},
	}
	for _, c := range cases {
		n, err := ParseLink(c.link)
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if n.Name != c.name || n.Out["type"] != c.typ {
			t.Errorf("%s: got %q %v", c.name, n.Name, n.Out["type"])
		}
		if c.check != nil {
			c.check(t, n.Out)
		}
	}
}

// Names may hold raw spaces, which must not split a link.
func TestLinkNameWithSpaces(t *testing.T) {
	n, err := Parse([]byte("trojan://pw@a.example.com:443#HK 01 IEPL\r\ntrojan://pw@b.example.com:443#JP 02\r\n"))
	if err != nil || len(n) != 2 || n[0].Name != "HK 01 IEPL" || n[1].Name != "JP 02" {
		t.Errorf("%v %v", n, err)
	}
}

func TestParseFormats(t *testing.T) {
	links := "trojan://pw@a.example.com:443#a\nss://" + b64("aes-128-gcm:x") + "@b.example.com:1#b\nbogus://x\n"
	for name, body := range map[string]string{"plain": links, "base64": b64(links)} {
		n, err := Parse([]byte(body))
		if err != nil || len(n) != 2 {
			t.Errorf("%s: %d nodes, err %v", name, len(n), err)
		}
	}
	clash := `
proxies:
  - {name: ws-node, type: vmess, server: c.example.com, port: 443, uuid: u, alterId: 0, cipher: auto, tls: true, network: ws, ws-opts: {path: /p, headers: {Host: h.com}}}
  - {name: rl, type: vless, server: d.example.com, port: 443, uuid: u, tls: true, flow: xtls-rprx-vision, client-fingerprint: chrome, servername: apple.com, reality-opts: {public-key: K, short-id: S}}
  - {name: hy, type: hysteria2, server: e.example.com, port: 443, password: p, sni: e.example.com, skip-cert-verify: true}
  - {name: unknown, type: wireguard, server: f, port: 1}
proxy-groups: []
`
	n, err := Parse([]byte(clash))
	if err != nil || len(n) != 3 {
		t.Fatalf("clash: %d nodes, err %v", len(n), err)
	}
	if n[0].Out["transport"].(map[string]any)["headers"].(map[string]any)["Host"] != "h.com" {
		t.Errorf("clash ws: %v", n[0].Out)
	}
	if n[1].Out["tls"].(map[string]any)["reality"].(map[string]any)["public_key"] != "K" {
		t.Errorf("clash reality: %v", n[1].Out)
	}

	sb := `{"outbounds":[{"type":"direct","tag":"direct"},{"type":"trojan","tag":"j","server":"g","server_port":1,"password":"p"},{"type":"selector","tag":"s","outbounds":["j"]}]}`
	n, err = Parse([]byte(sb))
	if err != nil || len(n) != 1 || n[0].Name != "j" {
		t.Errorf("sing-box json: %v %v", n, err)
	}
	if _, err := Parse([]byte("  ")); err == nil {
		t.Error("empty body parsed")
	}
	if _, err := Parse([]byte("<html>nope</html>")); err == nil {
		t.Error("html parsed")
	}
}

// A provider that refuses a request answers with a notice dressed as a node.
func TestNoticeInPlaceOfNodes(t *testing.T) {
	clash := "proxies:\n  - name: \"访问受限 REF: abc123\"\n    type: trojan\n    server: 127.0.0.1\n    port: 1\n    password: blocked\n"
	_, err := Parse([]byte(clash))
	if err == nil || !strings.Contains(err.Error(), "访问受限 REF: abc123") || !strings.Contains(err.Error(), "notice") {
		t.Errorf("a notice was taken for nodes: %v", err)
	}
	// The same in share links, and in sing-box JSON.
	if _, err := Parse([]byte("trojan://blocked@127.0.0.1:1#访问受限\n")); err == nil {
		t.Error("a link to localhost was taken for a node")
	}
	if _, err := Parse([]byte(`{"outbounds":[{"type":"trojan","tag":"expired","server":"0.0.0.0","server_port":1,"password":"x"}]}`)); err == nil {
		t.Error("a JSON notice was taken for a node")
	}
	// Notices mixed with real nodes are dropped, the nodes kept.
	mixed := "proxies:\n  - {name: 套餐已到期, type: trojan, server: 127.0.0.1, port: 1, password: x}\n  - {name: real, type: trojan, server: h.example.com, port: 443, password: p}\n"
	n, err := Parse([]byte(mixed))
	if err != nil || len(n) != 1 || n[0].Name != "real" {
		t.Errorf("%v %v", n, err)
	}
}
