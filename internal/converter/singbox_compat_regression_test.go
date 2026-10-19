package converter

import (
	"strings"
	"testing"
)

// sing-box 真实客户端（1.14.3 stable）实测暴露的导出兼容性回归：
// 官方文档 https://sing-box.sagernet.org （outbound/snell、outbound/tuic、
// outbound/shadowsocks、shared/v2ray-transport）是唯一事实来源。
//
//  1. snell: 扁平 obfs_mode/obfs_host（v4），v6 用 mode；不是 obfs 对象
//  2. tuic: 官方无 token 字段；v4-token 节点（无 uuid）必须跳过
//  3. shadowsocks: plugin 仅 obfs-local/v2ray-plugin，plugin_opts 是字符串
//  4. xhttp 等 sing-box 尚不支持的传输：整节点跳过（否则客户端配置解析失败）
func TestSingboxSnellFlatObfsFields(t *testing.T) {
	p := map[string]interface{}{
		"type": "snell", "name": "snell", "server": "127.0.0.1", "port": 20022,
		"psk": "snell-psk-123", "version": 4,
		"obfs-opts": map[string]interface{}{"mode": "http", "host": "cdn.example.com"},
	}
	ob, _ := mihomoProxyToSingbox(p)
	if ob == nil {
		t.Fatal("snell must be exported to sing-box subscription (1.14+ native)")
	}
	if v, _ := ob["obfs_mode"].(string); v != "http" {
		t.Errorf("obfs_mode = %q, want http (flat field per official docs)", v)
	}
	if v, _ := ob["obfs_host"].(string); v != "cdn.example.com" {
		t.Errorf("obfs_host = %q, want cdn.example.com", v)
	}
	if _, bad := ob["obfs"]; bad {
		t.Errorf("obfs object must not be emitted: %v", ob["obfs"])
	}
	if v, _ := ob["psk"].(string); v != "snell-psk-123" {
		t.Errorf("psk = %q, want snell-psk-123", v)
	}
}

func TestSingboxTuicTokenSkipped(t *testing.T) {
	// v4 token 认证：官方 tuic outbound 无 token 字段且 uuid 必填 → 跳过
	p := map[string]interface{}{
		"type": "tuic", "name": "tuic-v4", "server": "127.0.0.1", "port": 20019,
		"token": []string{"token-abc-123"},
	}
	ob, _ := mihomoProxyToSingbox(p)
	if ob != nil {
		t.Fatalf("tuic v4 token node must be skipped (sing-box has no token field): %v", ob)
	}
}

func TestSingboxTuicV5UUIDExported(t *testing.T) {
	// v5 uuid+password：正常导出
	p := map[string]interface{}{
		"type": "tuic", "name": "tuic-v5", "server": "127.0.0.1", "port": 20020,
		"uuid": "c5d1300d-dd7d-4b54-a194-2b2b7332986c", "password": "pw",
	}
	ob, tag := mihomoProxyToSingbox(p)
	if ob == nil {
		t.Fatal("tuic v5 (uuid+password) must be exported")
	}
	if tag != "tuic-v5" {
		t.Errorf("tag = %q", tag)
	}
	if v, _ := ob["uuid"].(string); v != "c5d1300d-dd7d-4b54-a194-2b2b7332986c" {
		t.Errorf("uuid = %q", v)
	}
	if _, bad := ob["token"]; bad {
		t.Errorf("token must not be emitted: %v", ob["token"])
	}
}

func TestSingboxSSPluginOptsString(t *testing.T) {
	p := map[string]interface{}{
		"type": "shadowsocks", "name": "ss-obfs", "server": "127.0.0.1", "port": 20017,
		"cipher": "aes-128-gcm", "password": "4p2YYLesNRMKJNfC",
		"plugin":      "obfs",
		"plugin-opts": map[string]interface{}{"mode": "http", "host": "cdn.example.com"},
	}
	ob, _ := mihomoProxyToSingbox(p)
	if ob == nil {
		t.Fatal("ss must be exported")
	}
	if v, _ := ob["plugin"].(string); v != "obfs-local" {
		t.Errorf("plugin = %q, want obfs-local (sing-box only knows obfs-local/v2ray-plugin)", v)
	}
	opts, ok := ob["plugin_opts"].(string)
	if !ok {
		t.Fatalf("plugin_opts must be a string, got %T (%v)", ob["plugin_opts"], ob["plugin_opts"])
	}
	if !strings.Contains(opts, "obfs=http") || !strings.Contains(opts, "obfs-host=cdn.example.com") {
		t.Errorf("plugin_opts = %q, want SIP003 string obfs=http;obfs-host=cdn.example.com", opts)
	}
}

func TestSingboxUnsupportedTransportSkipped(t *testing.T) {
	// xhttp：sing-box 官方 shared/v2ray-transport 文档无此类型（ws/http/
	// httpupgrade/grpc/quic）→ 整节点跳过，否则客户端整份配置解析失败。
	base := map[string]interface{}{
		"type": "vless", "name": "less-xhttp", "server": "127.0.0.1", "port": 20014,
		"uuid": "c5d1300d-dd7d-4b54-a194-2b2b7332986c", "network": "xhttp", "flow": "",
	}
	ob, _ := mihomoProxyToSingbox(base)
	if ob != nil {
		t.Errorf("xhttp node must be skipped (sing-box has no xhttp transport): %v", ob)
	}
	// ws/grpc 必须保留
	ws := map[string]interface{}{}
	for k, v := range base {
		ws[k] = v
	}
	ws["network"] = "ws"
	if ob, _ := mihomoProxyToSingbox(ws); ob == nil {
		t.Error("ws node must be exported")
	}
	g := map[string]interface{}{}
	for k, v := range base {
		g[k] = v
	}
	g["network"] = "grpc"
	if ob, _ := mihomoProxyToSingbox(g); ob == nil {
		t.Error("grpc node must be exported")
	}
}

func TestSingboxPluginAndOptsConversion(t *testing.T) {
	plugin, opts := singboxPluginAndOpts("obfs", map[string]interface{}{
		"mode": "http", "host": "cdn.example.com"})
	if plugin != "obfs-local" {
		t.Errorf("plugin alias obfs must map to obfs-local, got %q", plugin)
	}
	if opts != "obfs=http;obfs-host=cdn.example.com" {
		t.Errorf("opts = %q, want obfs=http;obfs-host=cdn.example.com", opts)
	}
	plugin2, opts2 := singboxPluginAndOpts("v2ray-plugin", map[string]interface{}{
		"mode": "websocket", "host": "cdn.example.com", "path": "/ws", "tls": true})
	if plugin2 != "v2ray-plugin" || opts2 != "mode=websocket;host=cdn.example.com;path=/ws;tls=1" {
		t.Errorf("v2ray-plugin: %q %q", plugin2, opts2)
	}
	if !singboxTransportSupported("ws") || !singboxTransportSupported("grpc") ||
		!singboxTransportSupported("http") || !singboxTransportSupported("httpupgrade") ||
		!singboxTransportSupported("quic") {
		t.Error("official v2ray-transport types must be supported")
	}
	if singboxTransportSupported("xhttp") || singboxTransportSupported("kcp-tun") {
		t.Error("xhttp/kcp-tun must not be claimed supported")
	}
}
