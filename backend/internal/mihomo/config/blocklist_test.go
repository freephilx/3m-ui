package config

import (
	"strings"
	"testing"

	"github.com/kazeyukiro/3m-ui/backend/internal/database"
)

func TestNormalizeBlockEntryAutoDetectsType(t *testing.T) {
	cases := []struct {
		in      BlockEntry
		wantTyp string
		wantVal string
	}{
		{BlockEntry{Value: "example.com"}, BlockTypeDomainSuffix, "example.com"},
		{BlockEntry{Value: "*.Example.com"}, BlockTypeDomainSuffix, "example.com"},
		{BlockEntry{Value: ".example.com"}, BlockTypeDomainSuffix, "example.com"},
		{BlockEntry{Type: BlockTypeDomain, Value: "exact.example.com"}, BlockTypeDomain, "exact.example.com"},
		{BlockEntry{Type: BlockTypeDomainKeyword, Value: "cdn"}, BlockTypeDomainKeyword, "cdn"},
		{BlockEntry{Type: BlockTypeGeosite, Value: "Google"}, BlockTypeGeosite, "google"},
		{BlockEntry{Value: "1.2.3.4"}, BlockTypeIPCIDR, "1.2.3.4/32"},
		{BlockEntry{Value: "10.0.0.0/8"}, BlockTypeIPCIDR, "10.0.0.0/8"},
		{BlockEntry{Value: "2001:db8::1"}, BlockTypeIPCIDR, "2001:db8::1/128"},
		{BlockEntry{Type: BlockTypeIPCIDR, Value: "9.9.9.9"}, BlockTypeIPCIDR, "9.9.9.9/32"},
	}
	for _, tc := range cases {
		got, err := normalizeBlockEntry(tc.in)
		if err != nil {
			t.Fatalf("%q: unexpected error: %v", tc.in.Value, err)
		}
		if got.Type != tc.wantTyp || got.Value != tc.wantVal {
			t.Fatalf("%q: got %s/%s, want %s/%s", tc.in.Value, got.Type, got.Value, tc.wantTyp, tc.wantVal)
		}
	}
}

func TestNormalizeBlocklistRejectsBadEntries(t *testing.T) {
	bad := []BlockEntry{
		{Value: ""},
		{Value: "a,b.com"},
		{Value: "has space.com"},
		{Value: "999.999.999.999"},
		{Value: "10.0.0.0/33"},
		{Value: "-bad-.com"},
		{Value: "example.com", Type: "wormhole"},
	}
	for _, e := range bad {
		if _, err := normalizeBlockEntry(e); err == nil {
			t.Fatalf("entry %+v was accepted", e)
		}
	}
}

func TestNormalizeBlocklistDedupesAndCaps(t *testing.T) {
	in := []BlockEntry{
		{Value: "example.com"},
		{Value: "EXAMPLE.com"},
		{Value: "*.example.com"},
		{Value: "other.com"},
	}
	out, err := NormalizeBlocklist(in)
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("expected 2 deduped entries, got %d: %+v", len(out), out)
	}
	if _, err := NormalizeBlocklist(make([]BlockEntry, maxBlocklistEntries+1)); err == nil {
		t.Fatal("oversized blocklist was accepted")
	}
}

func TestBlocklistRulesRenderAndSkipDisabled(t *testing.T) {
	entries := []BlockEntry{
		{Type: BlockTypeDomainSuffix, Value: "ads.example.com", Enabled: true},
		{Type: BlockTypeDomain, Value: "exact.example.com", Enabled: true},
		{Type: BlockTypeDomainKeyword, Value: "tracker", Enabled: true},
		{Type: BlockTypeGeosite, Value: "cn", Enabled: true},
		{Type: BlockTypeIPCIDR, Value: "10.0.0.0/8", Enabled: true},
		{Type: BlockTypeDomainSuffix, Value: "off.example.com", Enabled: false},
	}
	want := []string{
		"DOMAIN-SUFFIX,ads.example.com,REJECT",
		"DOMAIN,exact.example.com,REJECT",
		"DOMAIN-KEYWORD,tracker,REJECT",
		"GEOSITE,cn,REJECT",
		"IP-CIDR,10.0.0.0/8,REJECT",
	}
	got := BlocklistRules(entries)
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("rule %d: got %q, want %q", i, got[i], want[i])
		}
	}
}

func TestApplyBlocklistPrependsRulesAndArmsSniffer(t *testing.T) {
	merged := map[string]interface{}{
		"rules": []interface{}{"DOMAIN-SUFFIX,cloudflareclient.com,DIRECT", "MATCH,DIRECT"},
	}
	applyBlocklist(merged, []BlockEntry{
		{Type: BlockTypeDomainSuffix, Value: "blocked.example.com", Enabled: true},
		{Type: BlockTypeIPCIDR, Value: "10.0.0.0/8", Enabled: true},
	})
	rules, _ := merged["rules"].([]interface{})
	if len(rules) != 4 {
		t.Fatalf("expected 4 rules, got %v", rules)
	}
	if rules[0] != "DOMAIN-SUFFIX,blocked.example.com,REJECT" {
		t.Fatalf("block rule must be first, got %v", rules[0])
	}
	if rules[1] != "IP-CIDR,10.0.0.0/8,REJECT" {
		t.Fatalf("ip block rule must be second, got %v", rules[1])
	}
	sniffer, ok := merged["sniffer"].(map[string]interface{})
	if !ok || sniffer["enable"] != true {
		t.Fatalf("sniffer must be armed for domain rules: %v", merged["sniffer"])
	}
	dns, _ := merged["dns"].(map[string]interface{})
	if dns == nil || dns["enable"] != true {
		t.Fatalf("dns must be enabled for domain rules: %v", merged["dns"])
	}

	// IP-only blocklist must not arm the sniffer (nothing to sniff for),
	// but DNS must be enabled so the engine can resolve names at match time.
	merged2 := map[string]interface{}{"rules": []interface{}{"MATCH,DIRECT"}}
	applyBlocklist(merged2, []BlockEntry{{Type: BlockTypeIPCIDR, Value: "10.0.0.0/8", Enabled: true}})
	if _, armed := merged2["sniffer"]; armed {
		t.Fatal("ip-only blocklist must not arm the sniffer")
	}
	dns2, _ := merged2["dns"].(map[string]interface{})
	if dns2 == nil || dns2["enable"] != true {
		t.Fatalf("dns must be enabled for ip rules: %v", merged2["dns"])
	}

	// Empty blocklist is a no-op.
	merged3 := map[string]interface{}{"rules": []interface{}{"MATCH,DIRECT"}}
	applyBlocklist(merged3, nil)
	if rules3, _ := merged3["rules"].([]interface{}); len(rules3) != 1 {
		t.Fatalf("empty blocklist changed rules: %v", rules3)
	}
}

func TestParseBlocklistTextPrefixesAndRemarks(t *testing.T) {
	entries, err := ParseBlocklistText(`
# a comment line
ads.example.com
full:exact.example.com # exact match
keyword:tracker
geosite:google
ip:1.2.3.4
10.0.0.0/8

`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(entries) != 6 {
		t.Fatalf("expected 6 entries, got %d: %+v", len(entries), entries)
	}
	if entries[0].Type != BlockTypeDomainSuffix || entries[0].Value != "ads.example.com" {
		t.Fatalf("line 1: %+v", entries[0])
	}
	if entries[1].Type != BlockTypeDomain || entries[1].Value != "exact.example.com" || entries[1].Remark != "exact match" {
		t.Fatalf("line 2: %+v", entries[1])
	}
	if entries[4].Type != BlockTypeIPCIDR || entries[4].Value != "1.2.3.4/32" {
		t.Fatalf("line 5: %+v", entries[4])
	}
	if _, err := ParseBlocklistText("not a domain!"); err == nil {
		t.Fatal("invalid import line was accepted")
	}
}

func TestBlocklistSaveGetRoundTrip(t *testing.T) {
	db, err := database.InitDB(t.TempDir() + "/blocklist-test.db")
	if err != nil {
		t.Fatalf("init db: %v", err)
	}
	entries, err := ParseBlocklistText("ads.example.com\nip:1.2.3.4")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := SaveBlocklist(db, entries); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := GetBlocklist(db)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(got) != 2 || got[0].Value != "ads.example.com" || got[1].Value != "1.2.3.4/32" {
		t.Fatalf("round trip mismatch: %+v", got)
	}
	// Empty DB must read back an empty list, not an error.
	db2, err := database.InitDB(t.TempDir() + "/blocklist-empty.db")
	if err != nil {
		t.Fatalf("init db2: %v", err)
	}
	got2, err := GetBlocklist(db2)
	if err != nil || len(got2) != 0 {
		t.Fatalf("empty db: %v %+v", err, got2)
	}
}

func TestGenerateFinalConfigIncludesBlocklistRules(t *testing.T) {
	db, err := database.InitDB(t.TempDir() + "/blocklist-gen.db")
	if err != nil {
		t.Fatalf("init db: %v", err)
	}
	if err := SaveBlocklist(db, []BlockEntry{
		{Type: BlockTypeDomainSuffix, Value: "blocked.example.com", Enabled: true},
		{Type: BlockTypeIPCIDR, Value: "10.0.0.0/8", Enabled: true},
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	engine := NewConfigEngine(db)
	out, err := engine.GenerateFinalConfig()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if !strings.Contains(out, "DOMAIN-SUFFIX,blocked.example.com,REJECT") {
		t.Fatalf("domain block rule missing from serving config:\n%s", out)
	}
	if !strings.Contains(out, "IP-CIDR,10.0.0.0/8,REJECT") {
		t.Fatalf("ip block rule missing from serving config:\n%s", out)
	}
	// The block rule must precede the catch-all MATCH.
	if strings.Index(out, "DOMAIN-SUFFIX,blocked.example.com,REJECT") > strings.Index(out, "MATCH,DIRECT") {
		t.Fatal("block rule must precede MATCH,DIRECT")
	}
}
