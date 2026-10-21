package converter

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/kazeyukiro/3m-ui/backend/internal/database/models"
	"github.com/kazeyukiro/3m-ui/backend/internal/user"
	"gorm.io/gorm"
)

// client_apple.go implements native subscription exports for Loon, Surge and
// Quantumult X (QX). All three clients grew node-level parsing of the same
// share links the panel already emits for v2ray-style clients, so the export
// pipeline reuses GenerateUserBase64Subscription and re-renders each parsed
// share node into the target client's configuration grammar:
//
//   - Loon:  [Proxy] line grammar per nsloon.app/en/docs/Node
//   - Surge: [Proxy] line grammar per manual.nssurge.com
//   - QX:    [server_local] line grammar per Quantumult X sample.conf
//
// Clients that cannot speak a protocol (QX has no Hysteria2/TUIC, Loon has no
// TUIC) get a commented-out entry instead of a silently broken node.

// shareNode is a protocol-agnostic share link representation. The Names /
// Params keys follow the panel's own URI export vocabulary (see
// internal/node/uri_builders.go): insecure/allowInsecure, sni, alpn, obfs,
// obfs-password, flow, pbk, sid, type/path/host, up/down, fp, plugin, security.
type shareNode struct {
	Name     string
	Protocol string // ss, vmess, vless, trojan, hysteria2, tuic, anytls
	User     string // userinfo before ':' (tuic uuid, ss userpass mode)
	Password string // userinfo after ':' (hysteria2/trojan/vless/anytls password, ss password)
	Method   string // shadowsocks cipher
	Host     string
	Port     string
	Params   map[string]string

	// VMess links are base64 JSON instead of userinfo@host; the decoded object
	// is kept verbatim so renderers can read add/port/id/aid/scy/net/tls/sni.
	VMess *vmessLink
}

type vmessLink struct {
	Add  string `json:"add"`
	Port string `json:"port"`
	ID   string `json:"id"`
	AID  string `json:"aid"`
	Scy  string `json:"scy"`
	Net  string `json:"net"`
	TLS  string `json:"tls"`
	SNI  string `json:"sni"`
	Host string `json:"host"`
	Path string `json:"path"`
	PS   string `json:"ps"`
}

// parseShareURI decodes one of the panel's own share links. It is intentionally
// lenient: unknown schemes return an error the caller turns into a skip comment.
func parseShareURI(uri string) (*shareNode, error) {
	uri = strings.TrimSpace(uri)
	if uri == "" {
		return nil, fmt.Errorf("empty uri")
	}
	scheme, rest, ok := strings.Cut(uri, "://")
	if !ok {
		return nil, fmt.Errorf("missing scheme: %s", uri)
	}
	proto := strings.ToLower(scheme)
	// Split the fragment (display name) off before the query string: the
	// fragment offsets differ between the full URI and rest, so cut it here.
	var name string
	if i := strings.Index(rest, "#"); i >= 0 {
		if n, err := url.PathUnescape(rest[i+1:]); err == nil {
			name = n
		}
		rest = rest[:i]
	}
	// Query string.
	var query string
	if i := strings.Index(rest, "?"); i >= 0 {
		query = rest[i+1:]
		rest = rest[:i]
	}
	params := map[string]string{}
	if query != "" {
		for _, kv := range strings.Split(query, "&") {
			if kv == "" {
				continue
			}
			k, v, _ := strings.Cut(kv, "=")
			if k == "" {
				continue
			}
			if dv, err := url.QueryUnescape(v); err == nil {
				v = dv
			}
			if _, dup := params[k]; !dup {
				params[lowerParamKey(proto, k)] = v
			}
		}
	}

	n := &shareNode{Params: params, Name: name}

	switch proto {
	case "hysteria2", "hy2":
		n.Protocol = "hysteria2"
	case "ss", "shadowsocks":
		n.Protocol = "ss"
	case "vmess":
		n.Protocol = "vmess"
	case "vless":
		n.Protocol = "vless"
	case "trojan":
		n.Protocol = "trojan"
	case "tuic":
		n.Protocol = "tuic"
	case "anytls":
		n.Protocol = "anytls"
	default:
		return nil, fmt.Errorf("unsupported scheme %q", scheme)
	}

	if proto == "vmess" {
		obj, err := decodeVMessLink(rest)
		if err != nil {
			return nil, err
		}
		n.VMess = obj
		n.Host = obj.Add
		n.Port = strings.TrimSpace(obj.Port)
		n.Password = obj.ID
		n.Method = obj.Scy
		if obj.PS != "" && name == "" {
			n.Name = obj.PS
		}
		if n.Method == "" {
			n.Method = "auto"
		}
		// Fold vmess JSON transport fields into the shared param vocabulary.
		if obj.Net == "ws" {
			n.Params["type"] = "ws"
			if obj.Path != "" {
				n.Params["path"] = obj.Path
			}
			if obj.Host != "" {
				n.Params["host"] = obj.Host
			}
		}
		if obj.TLS == "tls" {
			n.Params["security"] = "tls"
			if obj.SNI != "" {
				n.Params["sni"] = obj.SNI
			}
		}
		if obj.TLS == "reality" {
			n.Params["security"] = "reality"
			if obj.SNI != "" {
				n.Params["sni"] = obj.SNI
			}
		}
		return n, nil
	}

	// userinfo@host:port for every non-vmess scheme.
	var authority string
	if i := strings.LastIndex(rest, "@"); i >= 0 {
		authority = rest[i+1:]
		userinfo := rest[:i]
		if u, err := url.QueryUnescape(userinfo); err == nil {
			userinfo = u
		}
		if user, pass, hasColon := strings.Cut(userinfo, ":"); hasColon {
			n.User, n.Password = user, pass
		} else {
			// Single-secret protocols (hysteria2/trojan/vless/anytls) carry the
			// password as the whole userinfo; only tuic-style user:pass splits.
			n.Password = user
		}
		if proto == "ss" {
			// SIP002: userinfo is base64(method:password).
			if decoded, err := decodeBase64Any(userinfo); err == nil {
				if m, p, ok := strings.Cut(string(decoded), ":"); ok {
					n.Method, n.Password = m, p
				}
			} else if m, p, ok := strings.Cut(userinfo, ":"); ok {
				n.Method, n.Password = m, p
			}
		}
	} else {
		authority = rest
	}

	host, port, err := net.SplitHostPort(authority)
	if err != nil {
		// Host without a port — keep the raw authority and let the renderer fail.
		host = authority
	}
	n.Host = strings.Trim(host, "[]")
	n.Port = strings.TrimSpace(port)
	// Hysteria2 multi-port links ("host:port1,port2-port3") mean port hopping;
	// the first port is the dial port, the remainder is a hop range.
	if proto == "hysteria2" || proto == "hy2" {
		if i := strings.Index(n.Port, ","); i >= 0 {
			hop := n.Port[i+1:]
			n.Port = n.Port[:i]
			if _, exists := n.Params["mport"]; !exists {
				n.Params["mport"] = hop
			}
		}
	}
	if n.Name == "" {
		n.Name = fmt.Sprintf("%s %s", proto, n.Host)
	}
	return n, nil
}

// lowerParamKey normalises a few well-known case variants only; realcase keys
// are preserved so renderers read exactly what the URI encoder emitted.
func lowerParamKey(proto, k string) string {
	switch strings.ToLower(k) {
	case "allowinsecure":
		return "allowInsecure"
	case "obfspassword":
		return "obfs-password"
	case "download-bandwidth":
		return "download-bandwidth"
	}
	return k
}

func decodeBase64Any(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	s = strings.TrimRight(s, "=")
	encodings := []*base64.Encoding{base64.StdEncoding, base64.URLEncoding, base64.RawStdEncoding, base64.RawURLEncoding}
	var lastErr error
	for _, enc := range encodings {
		if out, err := enc.DecodeString(s); err == nil {
			return out, nil
		} else {
			lastErr = err
		}
	}
	return nil, lastErr
}

func decodeVMessLink(body string) (*vmessLink, error) {
	raw, err := decodeBase64Any(body)
	if err != nil {
		return nil, fmt.Errorf("vmess base64: %w", err)
	}
	var obj vmessLink
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, fmt.Errorf("vmess json: %w", err)
	}
	if obj.ID == "" || obj.Add == "" {
		return nil, fmt.Errorf("vmess link missing id/add")
	}
	return &obj, nil
}

// ---- shared helpers ----

func shareBool(params map[string]string, key string) bool {
	v, ok := params[key]
	if !ok {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// shareInsecure reports whether the export asked the client to skip TLS
// verification (self-signed / IP-literal certificates).
func shareInsecure(params map[string]string) bool {
	return shareBool(params, "insecure") || shareBool(params, "allowInsecure") || shareBool(params, "skip-cert-verify")
}

func shareParam(params map[string]string, keys ...string) string {
	for _, k := range keys {
		if v, ok := params[k]; ok {
			v = strings.TrimSpace(v)
			if v != "" {
				return v
			}
		}
	}
	return ""
}

// joinNodeLine renders the Loon/Surge "name = proto,server,port,…" grammar:
// the first element is the display name, the second is the protocol keyword.
func joinNodeLine(parts []string) string {
	if len(parts) == 0 {
		return ""
	}
	if len(parts) == 1 {
		return parts[0]
	}
	return parts[0] + " = " + strings.Join(parts[1:], ",")
}

// quoteValue wraps a node-line value in double quotes when the target grammar
// otherwise breaks: values containing a comma or a double quote.
func quoteValue(v string) string {
	if strings.ContainsAny(v, ",;\" ") {
		return `"` + strings.ReplaceAll(v, `"`, "") + `"`
	}
	return v
}

// mbpsValue extracts the first integer out of a bandwidth string ("30 Mbps").
func mbpsValue(v string) string {
	fields := strings.Fields(v)
	for _, f := range fields {
		if n, err := strconv.Atoi(f); err == nil && n > 0 {
			return strconv.Itoa(n)
		}
	}
	return ""
}

// commentLine renders a skipped node as a comment carrying the reason.
func commentLine(name, reason string) string {
	return fmt.Sprintf("# %s: %s", name, reason)
}

// ---- Loon ----

// renderLoonNode renders one share node in Loon [Proxy] grammar.
func renderLoonNode(n *shareNode) string {
	var b strings.Builder
	tag := quoteValue(n.Name)
	switch n.Protocol {
	case "hysteria2":
		parts := []string{tag, "Hysteria2", n.Host, n.Port, quoteValue(n.Password)}
		if sni := shareParam(n.Params, "sni", "peer"); sni != "" {
			parts = append(parts, "sni="+sni)
		}
		if shareInsecure(n.Params) {
			parts = append(parts, "skip-cert-verify=true")
		}
		if alpn := shareParam(n.Params, "alpn"); alpn != "" {
			parts = append(parts, "alpn="+quoteValue(alpn))
		}
		if obfs := shareParam(n.Params, "obfs"); obfs == "salamander" {
			if pw := shareParam(n.Params, "obfs-password"); pw != "" {
				parts = append(parts, "salamander-password="+quoteValue(pw))
			}
		} else if obfs == "gecko" {
			if pw := shareParam(n.Params, "obfs-password"); pw != "" {
				parts = append(parts, "gecko-password="+quoteValue(pw))
			}
		}
		if hop := shareParam(n.Params, "mport"); hop != "" {
			parts = append(parts, "server-ports="+quoteValue(hopFormatSemicolon(hop)))
		}
		if bw := mbpsValue(shareParam(n.Params, "down", "up")); bw != "" {
			parts = append(parts, "download-bandwidth="+bw)
		}
		parts = append(parts, "udp=true", "block-quic=false")
		b.WriteString(joinNodeLine(parts))
	case "ss":
		if n.Method == "" {
			return ""
		}
		parts := []string{tag, "Shadowsocks", n.Host, n.Port, n.Method, quoteValue(n.Password)}
		parts = append(parts, loonSSPlugin(n.Params["plugin"])...)
		parts = append(parts, "udp=true", "block-quic=false")
		b.WriteString(joinNodeLine(parts))
	case "vmess":
		parts := []string{tag, "VMess", n.Host, n.Port, quoteValue(n.Method), quoteValue(n.Password)}
		transport := "tcp"
		if n.Params["type"] == "ws" {
			transport = "ws"
			parts = append(parts, "path="+quoteValue(shareParam(n.Params, "path")))
			if host := shareParam(n.Params, "host"); host != "" {
				parts = append(parts, "host="+host)
			}
		}
		parts = append(parts, "transport="+transport, "alterId=0")
		b.WriteString(loonTLSTail(parts, n.Params, shareParam(n.Params, "flow")))
	case "vless":
		parts := []string{tag, "VLESS", n.Host, n.Port, quoteValue(n.Password)}
		transport := "tcp"
		if n.Params["type"] == "ws" {
			transport = "ws"
			parts = append(parts, "path="+quoteValue(shareParam(n.Params, "path")))
			if host := shareParam(n.Params, "host"); host != "" {
				parts = append(parts, "host="+host)
			}
		}
		parts = append(parts, "transport="+transport)
		b.WriteString(loonTLSTail(parts, n.Params, shareParam(n.Params, "flow")))
	case "trojan":
		parts := []string{tag, "Trojan", n.Host, n.Port, quoteValue(n.Password)}
		transport := "tcp"
		if n.Params["type"] == "ws" {
			transport = "ws"
			parts = append(parts, "path="+quoteValue(shareParam(n.Params, "path")))
			if host := shareParam(n.Params, "host"); host != "" {
				parts = append(parts, "host="+host)
			}
		}
		parts = append(parts, "transport="+transport)
		b.WriteString(loonTLSTail(parts, n.Params, ""))
	case "anytls":
		parts := []string{tag, "AnyTLS", n.Host, n.Port, quoteValue(n.Password)}
		if sni := shareParam(n.Params, "sni"); sni != "" {
			parts = append(parts, "sni="+sni)
		}
		if shareInsecure(n.Params) {
			parts = append(parts, "skip-cert-verify=true")
		}
		parts = append(parts, "udp=true", "block-quic=false")
		b.WriteString(joinNodeLine(parts))
	default:
		return ""
	}
	return b.String()
}

// loonTLSTail appends the shared Loon TLS / Reality arguments to a node line.
func loonTLSTail(parts []string, params map[string]string, flow string) string {
	switch params["security"] {
	case "tls":
		parts = append(parts, "over-tls=true")
	case "reality":
		parts = append(parts, "over-tls=true")
		if pbk := shareParam(params, "pbk"); pbk != "" {
			parts = append(parts, "public-key="+quoteValue(pbk))
		}
		if sid := shareParam(params, "sid"); sid != "" {
			parts = append(parts, "short-id="+sid)
		}
	}
	if flow != "" {
		parts = append(parts, "flow="+flow)
	}
	if sni := shareParam(params, "sni"); sni != "" {
		parts = append(parts, "sni="+sni)
	}
	if shareInsecure(params) {
		parts = append(parts, "skip-cert-verify=true")
	}
	if pbk := shareParam(params, "pbk"); pbk != "" && params["security"] != "reality" {
		parts = append(parts, "public-key="+quoteValue(pbk))
	}
	parts = append(parts, "udp=true", "block-quic=false")
	return joinNodeLine(parts)
}

// loonSSPlugin maps a SIP003 plugin string (obfs-local;obfs=http;obfs-host=…)
// onto Loon's obfs-name/obfs-host/obfs-uri arguments.
func loonSSPlugin(plugin string) []string {
	if plugin == "" {
		return nil
	}
	out := []string{}
	for _, seg := range strings.Split(plugin, ";") {
		seg = strings.TrimSpace(seg)
		if seg == "" {
			continue
		}
		if strings.EqualFold(seg, "obfs-local") {
			continue
		}
		k, v, ok := strings.Cut(seg, "=")
		if !ok {
			k, v = seg, ""
		}
		switch strings.ToLower(k) {
		case "obfs":
			if v != "" {
				out = append(out, "obfs-name="+v)
			}
		case "obfs-host":
			if v != "" {
				out = append(out, "obfs-host="+quoteValue(v))
			}
		case "obfs-uri":
			if v != "" {
				out = append(out, "obfs-uri="+quoteValue(v))
			}
		}
	}
	return out
}

// hopFormatSemicolon converts a comma hop list ("443,5000-6000") to the
// semicolon grammar Loon/Surge expect.
func hopFormatSemicolon(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	if !strings.Contains(v, ";") {
		return strings.ReplaceAll(v, ",", ";")
	}
	return v
}

// ---- Surge ----

// renderSurgeNode renders one share node in Surge [Proxy] grammar.
func renderSurgeNode(n *shareNode) string {
	var b strings.Builder
	tag := quoteValue(n.Name)
	switch n.Protocol {
	case "hysteria2":
		parts := []string{tag, "hysteria2", n.Host, n.Port, "password=" + quoteValue(n.Password)}
		if sni := shareParam(n.Params, "sni", "peer"); sni != "" {
			parts = append(parts, "sni="+sni)
		}
		if shareInsecure(n.Params) {
			parts = append(parts, "skip-cert-verify=true")
		}
		if alpn := shareParam(n.Params, "alpn"); alpn != "" {
			parts = append(parts, "alpn="+quoteValue(alpn))
		}
		if obfs := shareParam(n.Params, "obfs"); obfs == "salamander" {
			if pw := shareParam(n.Params, "obfs-password"); pw != "" {
				parts = append(parts, "salamander-password="+quoteValue(pw))
			}
		} else if obfs == "gecko" {
			if pw := shareParam(n.Params, "obfs-password"); pw != "" {
				parts = append(parts, "gecko-password="+quoteValue(pw))
			}
		}
		if hop := shareParam(n.Params, "mport"); hop != "" {
			parts = append(parts, "port-hopping="+quoteValue(hopFormatSemicolon(hop)))
		}
		if bw := mbpsValue(shareParam(n.Params, "down", "up")); bw != "" {
			parts = append(parts, "download-bandwidth="+bw)
		}
		b.WriteString(joinNodeLine(parts))
	case "ss":
		if n.Method == "" {
			return ""
		}
		parts := []string{tag, "ss", n.Host, n.Port, "encrypt-method=" + n.Method, "password=" + quoteValue(n.Password)}
		parts = append(parts, surgeSSPlugin(n.Params["plugin"])...)
		parts = append(parts, "udp-relay=true")
		b.WriteString(joinNodeLine(parts))
	case "vmess":
		// Surge vmess syntax: username=<UUID>[, encrypt-method=|vmess-aead=true].
		params := []string{"username=" + n.Password}
		switch strings.ToLower(n.Method) {
		case "chacha20-ietf-poly1305", "aes-128-gcm", "aes-192-gcm", "aes-256-gcm", "xchacha20-ietf-poly1305":
			params = append(params, "encrypt-method="+n.Method)
		default:
			// "" / auto / zero: Surge's default is aes-128-gcm with AEAD off;
			// our nodes always use the AEAD handshake.
			params = append(params, "vmess-aead=true")
		}
		if n.Params["type"] == "ws" {
			params = append(params, "ws=true")
			if p := shareParam(n.Params, "path"); p != "" {
				params = append(params, "ws-path="+quoteValue(p))
			}
			if h := shareParam(n.Params, "host"); h != "" {
				params = append(params, "ws-headers="+quoteValue("Host:"+h))
			}
		}
		if n.Params["security"] == "tls" {
			params = append(params, "tls=true")
			if sni := shareParam(n.Params, "sni"); sni != "" {
				params = append(params, "sni="+sni)
			}
			if shareInsecure(n.Params) {
				params = append(params, "skip-cert-verify=true")
			}
		}
		parts := append([]string{tag, "vmess", n.Host, n.Port}, params...)
		b.WriteString(joinNodeLine(parts))
	case "trojan":
		params := []string{"password=" + quoteValue(n.Password)}
		if n.Params["type"] == "ws" {
			params = append(params, "ws=true")
			if p := shareParam(n.Params, "path"); p != "" {
				params = append(params, "ws-path="+quoteValue(p))
			}
			if h := shareParam(n.Params, "host"); h != "" {
				params = append(params, "ws-headers="+quoteValue("Host:"+h))
			}
		}
		if sni := shareParam(n.Params, "sni"); sni != "" {
			params = append(params, "sni="+sni)
		}
		if shareInsecure(n.Params) {
			params = append(params, "skip-cert-verify=true")
		}
		parts := append([]string{tag, "trojan", n.Host, n.Port}, params...)
		b.WriteString(joinNodeLine(parts))
	case "anytls":
		parts := []string{tag, "anytls", n.Host, n.Port, "password=" + quoteValue(n.Password)}
		if sni := shareParam(n.Params, "sni"); sni != "" {
			parts = append(parts, "sni="+sni)
		}
		if shareInsecure(n.Params) {
			parts = append(parts, "skip-cert-verify=true")
		}
		b.WriteString(joinNodeLine(parts))
	case "tuic":
		// Surge splits TUIC into two keywords: tuic (v4, token) and
		// tuic-v5 (v5, uuid + password). The v5 share URI carries the UUID
		// in userinfo, the v4 URI carries the bare token as the password.
		kw := "tuic"
		parts := []string{tag, kw, n.Host, n.Port}
		if n.User != "" {
			parts[1] = "tuic-v5"
			parts = append(parts, "uuid="+n.User, "password="+quoteValue(n.Password))
		} else if n.Password != "" {
			parts = append(parts, "token="+quoteValue(n.Password))
		}
		if alpn := shareParam(n.Params, "alpn"); alpn != "" {
			parts = append(parts, "alpn="+quoteValue(alpn))
		} else {
			parts = append(parts, "alpn=h3")
		}
		if sni := shareParam(n.Params, "sni"); sni != "" {
			parts = append(parts, "sni="+sni)
		}
		if shareInsecure(n.Params) {
			parts = append(parts, "skip-cert-verify=true")
		}
		if hop := shareParam(n.Params, "mport"); hop != "" {
			parts = append(parts, "port-hopping="+quoteValue(hopFormatSemicolon(hop)))
		}
		b.WriteString(joinNodeLine(parts))
	default:
		return ""
	}
	return b.String()
}

func surgeSSPlugin(plugin string) []string {
	out := []string{}
	if plugin == "" {
		return out
	}
	obfs, host, uri := "", "", ""
	for _, seg := range strings.Split(plugin, ";") {
		seg = strings.TrimSpace(seg)
		if strings.EqualFold(seg, "obfs-local") {
			continue
		}
		k, v, _ := strings.Cut(seg, "=")
		switch strings.ToLower(k) {
		case "obfs":
			obfs = v
		case "obfs-host":
			host = v
		case "obfs-uri":
			uri = v
		}
	}
	if obfs != "" && obfs != "none" {
		out = append(out, "obfs="+obfs)
	}
	if host != "" {
		out = append(out, "obfs-host="+quoteValue(host))
	}
	if uri != "" {
		out = append(out, "obfs-uri="+quoteValue(uri))
	}
	return out
}

// ---- Quantumult X ----

// renderQuanXNode renders one share node in QX [server_local] grammar.
func renderQuanXNode(n *shareNode) string {
	head := fmt.Sprintf("%s=%s:%s", n.Protocol, n.Host, n.Port)
	params := []string{}
	switch n.Protocol {
	case "ss":
		if n.Method == "" {
			return ""
		}
		params = append(params, "method="+n.Method, "password="+quoteValue(n.Password))
		params = append(params, quanxSSPlugin(n.Params["plugin"])...)
		params = append(params, "udp-relay=true", "tag="+quoteValue(n.Name))
	case "vmess":
		params = append(params, "method=none", "password="+quoteValue(n.Password))
		if n.Params["type"] == "ws" {
			params = append(params, "obfs=ws", "obfs-uri="+quoteValue(shareParam(n.Params, "path")))
			if h := shareParam(n.Params, "host"); h != "" {
				params = append(params, "obfs-host="+h)
			}
		}
		params = append(params, quanxTLS(n.Params)...)
		params = append(params, "udp-relay=true", "tag="+quoteValue(n.Name))
	case "vless":
		params = append(params, "method=none", "password="+quoteValue(n.Password))
		if flow := shareParam(n.Params, "flow"); flow != "" {
			params = append(params, "vless-flow="+flow)
		}
		if n.Params["type"] == "ws" {
			params = append(params, "obfs=ws", "obfs-uri="+quoteValue(shareParam(n.Params, "path")))
			if h := shareParam(n.Params, "host"); h != "" {
				params = append(params, "obfs-host="+h)
			}
		}
		params = append(params, quanxTLS(n.Params)...)
		params = append(params, "udp-relay=true", "tag="+quoteValue(n.Name))
	case "trojan":
		params = append(params, "password="+quoteValue(n.Password))
		if n.Params["type"] == "ws" {
			params = append(params, "obfs=ws", "obfs-uri="+quoteValue(shareParam(n.Params, "path")))
			if h := shareParam(n.Params, "host"); h != "" {
				params = append(params, "obfs-host="+h)
			}
		}
		params = append(params, quanxTLS(n.Params)...)
		params = append(params, "udp-relay=true", "tag="+quoteValue(n.Name))
	case "anytls":
		params = append(params, "password="+quoteValue(n.Password))
		params = append(params, quanxTLS(n.Params)...)
		params = append(params, "udp-relay=true", "tag="+quoteValue(n.Name))
	default:
		return ""
	}
	return head + ", " + strings.Join(params, ", ")
}

func quanxTLS(params map[string]string) []string {
	out := []string{}
	if params["security"] == "reality" {
		out = append(out, "over-tls=true")
		if pbk := shareParam(params, "pbk"); pbk != "" {
			out = append(out, "reality-base64-pubkey="+pbk)
		}
		if sid := shareParam(params, "sid"); sid != "" {
			out = append(out, "reality-hex-shortid="+sid)
		}
	} else if params["security"] == "tls" {
		out = append(out, "over-tls=true")
	}
	if sni := shareParam(params, "sni"); sni != "" {
		out = append(out, "tls-host="+sni)
	}
	out = append(out, "tls-verification="+strconv.FormatBool(!shareInsecure(params)))
	return out
}

func quanxSSPlugin(plugin string) []string {
	out := []string{}
	if plugin == "" {
		return out
	}
	obfs, host, uri := "", "", ""
	for _, seg := range strings.Split(plugin, ";") {
		seg = strings.TrimSpace(seg)
		if strings.EqualFold(seg, "obfs-local") {
			continue
		}
		k, v, _ := strings.Cut(seg, "=")
		switch strings.ToLower(k) {
		case "obfs":
			obfs = v
		case "obfs-host":
			host = v
		case "obfs-uri":
			uri = v
		}
	}
	if obfs != "" && obfs != "none" {
		out = append(out, "obfs="+obfs)
	}
	if host != "" {
		out = append(out, "obfs-host="+host)
	}
	if uri != "" {
		out = append(out, "obfs-uri="+quoteValue(uri))
	}
	return out
}

// ---- public generators ----

// clientShareLinks returns the decoded share links (one URI per line) for a
// proxy user: bound listeners, credentials and remote mirrors, using exactly
// the same resolution as the base64 subscription.
func clientShareLinks(db *gorm.DB, pu models.ProxyUser, req *http.Request, gen URIGenerator) ([]string, error) {
	raw, err := GenerateUserBase64Subscription(db, pu, req, gen)
	if err != nil {
		return nil, err
	}
	body := raw
	if decoded, decErr := base64.StdEncoding.DecodeString(string(raw)); decErr == nil {
		body = decoded
	}
	var links []string
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			links = append(links, line)
		}
	}
	if len(links) == 0 {
		return nil, fmt.Errorf("no exportable share links for user")
	}
	return links, nil
}

// appleClientTarget selects the target grammar for the shared generator.
type appleClientTarget struct {
	client  string                          // loon / surge / quanx
	render  func(*shareNode) string         // node-line renderer
	header  []string                        // section header lines
	footer  func(names []string) []string   // optional trail (Surge group)
	skipMsg func(*shareNode) (string, bool) // optional unsupported-protocol reason
}

func generateAppleClientSubscription(t appleClientTarget, db *gorm.DB, pu models.ProxyUser, req *http.Request, gen URIGenerator) ([]byte, error) {
	if db == nil {
		return nil, fmt.Errorf("database is not initialized")
	}
	if !user.IsCredentialActive(pu) {
		return nil, fmt.Errorf("user is not active")
	}
	links, err := clientShareLinks(db, pu, req, gen)
	if err != nil {
		return nil, err
	}

	lines := append([]string{}, t.header...)
	names := make([]string, 0, len(links))
	seen := map[string]int{}
	for _, link := range links {
		node, perr := parseShareURI(link)
		if perr != nil {
			lines = append(lines, fmt.Sprintf("# unparsable share link skipped: %v", perr))
			continue
		}
		name := node.Name
		if n, dup := seen[name]; dup {
			seen[name] = n + 1
			name = fmt.Sprintf("%s-%d", name, n+1)
			node.Name = name
		} else {
			seen[name] = 1
		}
		if t.skipMsg != nil {
			if reason, unsupported := t.skipMsg(node); unsupported {
				lines = append(lines, commentLine(name, reason))
				continue
			}
		}
		line := t.render(node)
		if line == "" {
			lines = append(lines, commentLine(name, t.client+" renderer could not represent this node"))
			continue
		}
		// Only nodes that actually rendered may be referenced by footer
		// sections (e.g. Surge's [Proxy Group]); dangling policy names make
		// real clients reject the whole profile.
		names = append(names, name)
		lines = append(lines, line)
	}
	if t.footer != nil {
		lines = append(lines, t.footer(names)...)
	}
	return []byte(strings.Join(lines, "\n") + "\n"), nil
}

// GenerateUserLoonSubscription exports the user's share links as a Loon
// subscription ([Proxy] section, one node per line).
func GenerateUserLoonSubscription(db *gorm.DB, pu models.ProxyUser, req *http.Request, gen URIGenerator) ([]byte, error) {
	return generateAppleClientSubscription(appleClientTarget{
		client: "loon",
		render: renderLoonNode,
		header: []string{"[Proxy]"},
		skipMsg: func(n *shareNode) (string, bool) {
			if n.Protocol == "tuic" {
				return "Loon 暂不支持 TUIC 协议", true
			}
			return "", false
		},
	}, db, pu, req, gen)
}

// surgeSkipMsg reports nodes Surge cannot express at all: VLESS (absent from
// Surge's protocol list) and reality transports (no public-key/short-id
// parameters exist in Surge's TLS argument set).
func surgeSkipMsg(n *shareNode) (string, bool) {
	switch n.Protocol {
	case "vless":
		return "Surge 不支持 VLESS 协议", true
	case "trojan", "vmess":
		if n.Params["security"] == "reality" {
			return "Surge 不支持 REALITY 传输", true
		}
	}
	return "", false
}

// GenerateUserSurgeSubscription exports the user's share links as a Surge
// profile ([General]/[Proxy]/[Proxy Group]/[Rule] mini profile with a select
// policy that points at every node).
func GenerateUserSurgeSubscription(db *gorm.DB, pu models.ProxyUser, req *http.Request, gen URIGenerator) ([]byte, error) {
	return generateAppleClientSubscription(appleClientTarget{
		client:  "surge",
		render:  renderSurgeNode,
		header:  []string{"[General]", "", "[Proxy]"},
		skipMsg: surgeSkipMsg,
		footer: func(names []string) []string {
			group := "3m-ui"
			kept := make([]string, 0, len(names))
			for _, name := range names {
				kept = append(kept, quoteValue(name))
			}
			return []string{
				"",
				"[Proxy Group]",
				fmt.Sprintf("%s = select, DIRECT, %s", group, strings.Join(kept, ", ")),
				"",
				"[Rule]",
				"FINAL," + group,
			}
		},
	}, db, pu, req, gen)
}

// GenerateUserQuanXSubscription exports the user's share links as a Quantumult
// X subscription ([server_local] section, one node per line).
func GenerateUserQuanXSubscription(db *gorm.DB, pu models.ProxyUser, req *http.Request, gen URIGenerator) ([]byte, error) {
	return generateAppleClientSubscription(appleClientTarget{
		client: "quanx",
		render: renderQuanXNode,
		header: []string{"[server_local]"},
		skipMsg: func(n *shareNode) (string, bool) {
			switch n.Protocol {
			case "hysteria2":
				return "Quantumult X 不支持 Hysteria2 协议", true
			case "tuic":
				return "Quantumult X 不支持 TUIC 协议", true
			}
			return "", false
		},
	}, db, pu, req, gen)
}
