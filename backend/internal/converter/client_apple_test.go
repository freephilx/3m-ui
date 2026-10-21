package converter

import (
	"strings"
	"testing"
)

func TestParseShareURIHysteria2(t *testing.T) {
	n, err := parseShareURI("hysteria2://CAikPKr_Q41vRdXNpDQwX3wL@85.149.212.214:13290#%E8%8A%82%E7%82%B92")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if n.Protocol != "hysteria2" || n.Password != "CAikPKr_Q41vRdXNpDQwX3wL" {
		t.Fatalf("proto/password = %q/%q", n.Protocol, n.Password)
	}
	if n.Host != "85.149.212.214" || n.Port != "13290" {
		t.Fatalf("host/port = %q/%q", n.Host, n.Port)
	}
	if n.Name != "节点2" {
		t.Fatalf("name = %q", n.Name)
	}
}

func TestParseShareURIHysteria2InsecureObfs(t *testing.T) {
	n, err := parseShareURI("hy2://pw@hy2.example.com:443/?sni=hy2.example.com&insecure=1&allowInsecure=1&obfs=salamander&obfs-password=obfspw123&alpn=h3&down=100+Mbps")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !shareInsecure(n.Params) {
		t.Fatalf("expected insecure true")
	}
	if n.Params["obfs-password"] != "obfspw123" {
		t.Fatalf("obfs-password = %q", n.Params["obfs-password"])
	}
	if mbpsValue(n.Params["down"]) != "100" {
		t.Fatalf("mbps = %q", mbpsValue(n.Params["down"]))
	}
}

func TestParseShareURIMultiPort(t *testing.T) {
	n, err := parseShareURI("hysteria2://pw@host.example.com:443,5000-6000#hop")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if n.Port != "443" || n.Params["mport"] != "5000-6000" {
		t.Fatalf("port/mport = %q/%q", n.Port, n.Params["mport"])
	}
}

func TestParseShareURIShadowSocks(t *testing.T) {
	n, err := parseShareURI("ss://YWVzLTEyOC1nY206cGFzc3dvcmQ=@ss.example.com:8388/?plugin=obfs-local%3Bobfs%3Dhttp%3Bobfs-host%3Dbing.com#ss-node")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if n.Protocol != "ss" || n.Method != "aes-128-gcm" || n.Password != "password" {
		t.Fatalf("ss = %q/%q/%q", n.Protocol, n.Method, n.Password)
	}
	if n.Params["plugin"] == "" || !strings.Contains(n.Params["plugin"], "obfs-local") {
		t.Fatalf("plugin = %q", n.Params["plugin"])
	}
	if n.Name != "ss-node" {
		t.Fatalf("name = %q", n.Name)
	}
}

func TestParseShareURIVMess(t *testing.T) {
	// vmess://base64({"v":"2","ps":"v1","add":"vm.example.com","port":"443","id":"52396e06-041a-4cc2-be5c-8525eb457809","aid":"0","scy":"auto","net":"ws","type":"none","tls":"tls","sni":"vm.example.com","host":"cdn.example.com","path":"/ws"})
	body := "eyJ2IjoiMiIsInBzIjoidjEiLCJhZGQiOiJ2bS5leGFtcGxlLmNvbSIsInBvcnQiOiI0NDMiLCJpZCI6IjUyMzk2ZTA2LTA0MWEtNGNjMi1iZTVjLTg1MjVlYjQ1NzgwOSIsImFpZCI6IjAiLCJzY3kiOiJhdXRvIiwibmV0Ijoid3MiLCJ0eXBlIjoibm9uZSIsInRscyI6InRscyIsInNuaSI6InZtLmV4YW1wbGUuY29tIiwiaG9zdCI6ImNkbi5leGFtcGxlLmNvbSIsInBhdGgiOiIvd3MifQ=="
	n, err := parseShareURI("vmess://" + body + "#v1")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if n.Protocol != "vmess" || n.Method != "auto" || n.Password != "52396e06-041a-4cc2-be5c-8525eb457809" {
		t.Fatalf("vmess = %q/%q/%q", n.Protocol, n.Method, n.Password)
	}
	if n.Params["type"] != "ws" || n.Params["path"] != "/ws" || n.Params["host"] != "cdn.example.com" {
		t.Fatalf("ws params = %v", n.Params)
	}
	if n.Params["security"] != "tls" || n.Params["sni"] != "vm.example.com" {
		t.Fatalf("tls params = %v", n.Params)
	}
}

func TestParseShareURIVLESSReality(t *testing.T) {
	n, err := parseShareURI("vless://52396e06-041a-4cc2-be5c-8525eb457809@vless.example.com:443?security=reality&pbk=PUBKEY&sid=4f3a&flow=xtls-rprx-vision&sni=www.example.com#reality")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if n.Protocol != "vless" || n.Password != "52396e06-041a-4cc2-be5c-8525eb457809" {
		t.Fatalf("vless = %q/%q", n.Protocol, n.Password)
	}
	if n.Params["pbk"] != "PUBKEY" || n.Params["sid"] != "4f3a" || n.Params["flow"] != "xtls-rprx-vision" {
		t.Fatalf("reality params = %v", n.Params)
	}
}

func TestParseShareURIIPv6(t *testing.T) {
	n, err := parseShareURI("hysteria2://pw@[2001:db8::1]:443?insecure=1#v6")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if n.Host != "2001:db8::1" || n.Port != "443" {
		t.Fatalf("v6 host/port = %q/%q", n.Host, n.Port)
	}
}

func TestRenderLoonHysteria2Salamander(t *testing.T) {
	n, _ := parseShareURI("hy2://pw@85.149.212.214:13290?sni=cdn.example.com&insecure=1&obfs=salamander&obfs-password=obfspw123&alpn=h3&down=200+Mbps#loon-node")
	got := renderLoonNode(n)
	want := `loon-node = Hysteria2,85.149.212.214,13290,pw,sni=cdn.example.com,skip-cert-verify=true,alpn=h3,salamander-password=obfspw123,download-bandwidth=200,udp=true,block-quic=false`
	if got != want {
		t.Fatalf("loon line:\n got %q\nwant %q", got, want)
	}
}

func TestRenderLoonHysteria2ServerPorts(t *testing.T) {
	// Loon documents server-ports as comma-separated entries with colon
	// ranges: server-ports="20000:20100". Dash ranges from the share URI
	// must be converted, not passed through (Surge syntax).
	n, _ := parseShareURI("hy2://pw@hy2.example.com:443,5000-6000?mport=443,5000-6000#hop")
	got := renderLoonNode(n)
	if !strings.Contains(got, `server-ports="443,5000:6000"`) {
		t.Fatalf("loon server-ports wrong format: %s", got)
	}
	if strings.Contains(got, "5000-6000") || strings.Contains(got, ";") {
		t.Fatalf("loon server-ports must not use dash/semicolon: %s", got)
	}
}

func TestRenderLoonHysteria2GeckoSkipped(t *testing.T) {
	// Loon has no gecko-password in its Hysteria 2 section: the renderer
	// emits nothing and the generator degrades it to a comment.
	n, _ := parseShareURI("hy2://pw@hy2.example.com:443?obfs=gecko&obfs-password=gpw#g")
	if got := renderLoonNode(n); got != "" {
		t.Fatalf("loon should not render gecko hy2, got %q", got)
	}
}

func TestRenderLoonAnyTLSReality(t *testing.T) {
	n, _ := parseShareURI("anytls://pw@at.example.com:443?security=reality&pbk=dR2euO5X-8ENLeBjO9ZfXQ2RDlg_uBEHBHBNBQTapyU&sid=a0997793b8119118&sni=www.microsoft.com#at-r")
	got := renderLoonNode(n)
	for _, frag := range []string{"AnyTLS,at.example.com,443", "public-key=dR2euO5X-8ENLeBjO9ZfXQ2RDlg_uBEHBHBNBQTapyU", "short-id=a0997793b8119118", "sni=www.microsoft.com"} {
		if !strings.Contains(got, frag) {
			t.Fatalf("loon anytls reality missing %q: %s", frag, got)
		}
	}
	if strings.Contains(got, "over-tls") {
		t.Fatalf("loon anytls must not carry over-tls: %s", got)
	}
}

func TestRenderLoonSSPlugin(t *testing.T) {
	n, _ := parseShareURI("ss://YWVzLTEyOC1nY206cGFzc3dvcmQ=@ss.example.com:8388/?plugin=obfs-local%3Bobfs%3Dhttp%3Bobfs-host%3Dbing.com#ss-node")
	got := renderLoonNode(n)
	if !strings.Contains(got, "obfs-name=http") || !strings.Contains(got, "obfs-host=bing.com") {
		t.Fatalf("loon ss = %q", got)
	}
}

func TestRenderLoonVLESSReality(t *testing.T) {
	n, _ := parseShareURI("vless://uuid@vless.example.com:443?security=reality&pbk=PUBKEY&sid=4f3a&flow=xtls-rprx-vision&sni=www.example.com#r")
	got := renderLoonNode(n)
	for _, frag := range []string{"VLESS", "transport=tcp", "over-tls=true", "public-key=PUBKEY", "short-id=4f3a", "flow=xtls-rprx-vision", "udp=true"} {
		if !strings.Contains(got, frag) {
			t.Fatalf("loon vless missing %q: %s", frag, got)
		}
	}
}

func TestRenderSurgeHysteria2(t *testing.T) {
	n, _ := parseShareURI("hy2://pw@hy2.example.com:443,1234;5000-6000?sni=hy2.example.com&insecure=1&obfs=salamander&obfs-password=obfspw123&down=100+Mbps#surge-node")
	got := renderSurgeNode(n)
	want := `surge-node = hysteria2,hy2.example.com,443,password=pw,sni=hy2.example.com,skip-cert-verify=true,salamander-password=obfspw123,port-hopping="1234;5000-6000",download-bandwidth=100`
	if got != want {
		t.Fatalf("surge line:\n got %q\nwant %q", got, want)
	}
}

func TestRenderSurgeVmessWS(t *testing.T) {
	n, _ := parseShareURI("vmess://eyJ2IjoiMiIsInBzIjoidjEiLCJhZGQiOiJ2bS5leGFtcGxlLmNvbSIsInBvcnQiOiI0NDMiLCJpZCI6IjUyMzk2ZTA2LTA0MWEtNGNjMi1iZTVjLTg1MjVlYjQ1NzgwOSIsImFpZCI6IjAiLCJzY3kiOiJhdXRvIiwibmV0Ijoid3MiLCJ0eXBlIjoibm9uZSIsInRscyI6InRscyIsInNuaSI6InZtLmV4YW1wbGUuY29tIiwiaG9zdCI6ImNkbi5leGFtcGxlLmNvbSIsInBhdGgiOiIvd3MifQ==#v1")
	got := renderSurgeNode(n)
	for _, frag := range []string{"vmess,vm.example.com,443", "username=52396e06", "vmess-aead=true", "ws=true", "ws-path=/ws", "ws-headers=Host:cdn.example.com", "tls=true"} {
		if !strings.Contains(got, frag) {
			t.Fatalf("surge vmess missing %q: %s", frag, got)
		}
	}
	if strings.Contains(got, "method=") || strings.Contains(got, "uuid=") {
		t.Fatalf("surge vmess should not use method=/uuid=: %s", got)
	}
}

func TestRenderSurgeShadowsocksKeyword(t *testing.T) {
	n, _ := parseShareURI("ss://YWVzLTEyOC1nY206cGFzcw==@ss.example.com:443#ss1")
	got := renderSurgeNode(n)
	want := `ss1 = ss,ss.example.com,443,encrypt-method=aes-128-gcm,password=pass,udp-relay=true`
	if got != want {
		t.Fatalf("surge ss line:\n got %q\nwant %q", got, want)
	}
}

func TestRenderSurgeTUICv5(t *testing.T) {
	n, _ := parseShareURI("tuic://91066351-60f7-44b9-ad7e-c4bcc82524a6:pw@tuic.example.com:443?alpn=h3&sni=tuic.example.com#t5")
	got := renderSurgeNode(n)
	if !strings.Contains(got, "tuic-v5,tuic.example.com,443") || !strings.Contains(got, "uuid=91066351") || !strings.Contains(got, "password=pw") || !strings.Contains(got, "alpn=h3") {
		t.Fatalf("surge tuic-v5 wrong: %s", got)
	}
	if strings.Contains(got, "token=") || strings.Contains(got, "udp-relay") {
		t.Fatalf("surge tuic-v5 must not carry token=/udp-relay: %s", got)
	}
}

func TestRenderSurgeTUICv4(t *testing.T) {
	n, _ := parseShareURI("tuic://token-abc@tuic.example.com:443?alpn=h3#t4")
	got := renderSurgeNode(n)
	if !strings.Contains(got, "tuic,tuic.example.com,443") || !strings.Contains(got, "token=token-abc") {
		t.Fatalf("surge tuic v4 wrong: %s", got)
	}
}

func TestSurgeSkipMsg(t *testing.T) {
	if reason, skip := surgeSkipMsg(&shareNode{Protocol: "vless", Params: map[string]string{}}); !skip || !strings.Contains(reason, "VLESS") {
		t.Fatalf("surge should skip vless: skip=%v reason=%q", skip, reason)
	}
	if reason, skip := surgeSkipMsg(&shareNode{Protocol: "trojan", Params: map[string]string{"security": "reality"}}); !skip || !strings.Contains(reason, "REALITY") {
		t.Fatalf("surge should skip reality trojan: skip=%v reason=%q", skip, reason)
	}
	if _, skip := surgeSkipMsg(&shareNode{Protocol: "hysteria2", Params: map[string]string{}}); skip {
		t.Fatal("surge should keep hysteria2")
	}
}

func TestRenderQuanXVmess(t *testing.T) {
	n, _ := parseShareURI("vmess://eyJ2IjoiMiIsInBzIjoidjEiLCJhZGQiOiJ2bS5leGFtcGxlLmNvbSIsInBvcnQiOiI0NDMiLCJpZCI6IjUyMzk2ZTA2LTA0MWEtNGNjMi1iZTVjLTg1MjVlYjQ1NzgwOSIsImFpZCI6IjAiLCJzY3kiOiJhdXRvIiwibmV0Ijoid3MiLCJ0eXBlIjoibm9uZSIsInRscyI6InRscyIsInNuaSI6InZtLmV4YW1wbGUuY29tIiwiaG9zdCI6ImNkbi5leGFtcGxlLmNvbSIsInBhdGgiOiIvd3MifQ==#v1")
	got := renderQuanXNode(n)
	for _, frag := range []string{"vmess=vm.example.com:443", "method=none", "password=52396e06", "obfs=ws", "obfs-uri=/ws", "over-tls=true", "tls-host=vm.example.com", "tag=v1"} {
		if !strings.Contains(got, frag) {
			t.Fatalf("qx vmess missing %q: %s", frag, got)
		}
	}
}

func TestRenderQuanXHysteria2Skipped(t *testing.T) {
	n, _ := parseShareURI("hy2://pw@85.149.212.214:13290#hy2")
	if renderQuanXNode(n) != "" {
		t.Fatalf("QX must not render hysteria2: %s", renderQuanXNode(n))
	}
}

func TestQuoteValue(t *testing.T) {
	cases := map[string]string{
		"plain": "plain",
		"a,b":   `"a,b"`,
		`x"y`:   `"xy"`,
	}
	for in, want := range cases {
		if got := quoteValue(in); got != want {
			t.Fatalf("quoteValue(%q) = %q want %q", in, got, want)
		}
	}
}

func TestHopFormatSemicolon(t *testing.T) {
	if got := hopFormatSemicolon("443,5000-6000"); got != "443;5000-6000" {
		t.Fatalf("hop = %q", got)
	}
	if got := hopFormatSemicolon("443;5000-6000"); got != "443;5000-6000" {
		t.Fatalf("hop = %q", got)
	}
}
