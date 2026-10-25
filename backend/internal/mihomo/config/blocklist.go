package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"

	"github.com/kazeyukiro/3m-ui/backend/internal/database/models"
	"gorm.io/gorm"
)

// blocklistSettingKey is the panel_settings key holding the website-audit
// blocklist as JSON. It is deliberately separate from server-routing: the
// routing editor persists the whole ServerRoutingConfig and would otherwise
// wipe blocklist rows saved by the audit page.
const blocklistSettingKey = "blocklist"

// maxBlocklistEntries caps the persisted list so a runaway import cannot
// bloat the generated Mihomo config (each row is one rule line).
const maxBlocklistEntries = 5000

// maxBlocklistValueLen caps a single value (domains are ≤253 chars per RFC).
const maxBlocklistValueLen = 253

// Block types. Type is auto-detected from Value when empty; the audit UI
// exposes the same set. Batch-import prefixes map onto them (see parseBlockLine).
const (
	BlockTypeDomain        = "domain"         // DOMAIN,exact.host,REJECT
	BlockTypeDomainSuffix  = "domain-suffix"  // DOMAIN-SUFFIX,zone,REJECT
	BlockTypeDomainKeyword = "domain-keyword" // DOMAIN-KEYWORD,substr,REJECT
	BlockTypeGeosite       = "geosite"        // GEOSITE,category,REJECT
	BlockTypeIPCIDR        = "ip-cidr"        // IP-CIDR,net,REJECT
)

// BlockEntry is one website-audit blocklist row: a domain (or geosite
// category) or an IP/CIDR that the serving Mihomo process must REJECT for
// every client behind a listener.
type BlockEntry struct {
	Type    string `json:"type" yaml:"type"`
	Value   string `json:"value" yaml:"value"`
	Remark  string `json:"remark,omitempty" yaml:"remark,omitempty"`
	Enabled bool   `json:"enabled" yaml:"enabled"`
}

// GetBlocklist loads the persisted blocklist. A missing row is an empty
// list, not an error.
func GetBlocklist(db *gorm.DB) ([]BlockEntry, error) {
	if db == nil {
		return nil, nil
	}
	var row models.PanelSetting
	err := db.Where("key = ?", blocklistSettingKey).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return []BlockEntry{}, nil
	}
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(row.Value) == "" {
		return []BlockEntry{}, nil
	}
	var entries []BlockEntry
	if err := json.Unmarshal([]byte(row.Value), &entries); err != nil {
		return nil, fmt.Errorf("parse blocklist: %w", err)
	}
	return entries, nil
}

// SaveBlocklist validates, normalizes and persists the blocklist.
func SaveBlocklist(db *gorm.DB, entries []BlockEntry) error {
	if db == nil {
		return fmt.Errorf("database is not initialized")
	}
	normalized, err := NormalizeBlocklist(entries)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(normalized)
	if err != nil {
		return err
	}
	var row models.PanelSetting
	// Soft-deleted rows still hold UNIQUE(key); must Unscoped.
	err = db.Unscoped().Where("key = ?", blocklistSettingKey).First(&row).Error
	if err == nil {
		row.Value = string(raw)
		row.DeletedAt = gorm.DeletedAt{}
		return db.Unscoped().Save(&row).Error
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	if err := db.Create(&models.PanelSetting{Key: blocklistSettingKey, Value: string(raw)}).Error; err != nil {
		var again models.PanelSetting
		if e2 := db.Unscoped().Where("key = ?", blocklistSettingKey).First(&again).Error; e2 == nil {
			again.Value = string(raw)
			again.DeletedAt = gorm.DeletedAt{}
			return db.Unscoped().Save(&again).Error
		}
		return err
	}
	return nil
}

// NormalizeBlocklist validates every entry, fills in the auto-detected type
// and drops duplicates (same type+value). The first occurrence wins.
func NormalizeBlocklist(entries []BlockEntry) ([]BlockEntry, error) {
	if len(entries) > maxBlocklistEntries {
		return nil, fmt.Errorf("blocklist exceeds %d entries", maxBlocklistEntries)
	}
	seen := make(map[string]struct{}, len(entries))
	out := make([]BlockEntry, 0, len(entries))
	for i, e := range entries {
		norm, err := normalizeBlockEntry(e)
		if err != nil {
			return nil, fmt.Errorf("blocklist entry #%d: %w", i+1, err)
		}
		key := norm.Type + "\x00" + norm.Value
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, norm)
	}
	return out, nil
}

// normalizeBlockEntry trims, auto-detects the type and validates one entry.
func normalizeBlockEntry(e BlockEntry) (BlockEntry, error) {
	typ := strings.ToLower(strings.TrimSpace(e.Type))
	value := strings.TrimSpace(e.Value)
	if value == "" {
		return BlockEntry{}, fmt.Errorf("value is empty")
	}
	if len(value) > maxBlocklistValueLen {
		return BlockEntry{}, fmt.Errorf("value %q exceeds %d characters", value, maxBlocklistValueLen)
	}
	if strings.ContainsAny(value, ", \t\r\n") {
		return BlockEntry{}, fmt.Errorf("value %q must not contain commas or whitespace", value)
	}
	if typ == "" {
		typ = detectBlockType(value)
	}
	// A value that looks like an IPv4 but does not parse is almost always a
	// typo'd IP; accepting it as a domain-suffix would silently never match.
	if typ != BlockTypeIPCIDR && looksLikeIPv4(value) {
		return BlockEntry{}, fmt.Errorf("%q looks like an invalid IPv4 address", value)
	}
	switch typ {
	case BlockTypeIPCIDR:
		value = normalizeCIDR(value)
		if _, _, err := net.ParseCIDR(value); err != nil {
			return BlockEntry{}, fmt.Errorf("invalid IP/CIDR %q", value)
		}
	case BlockTypeDomain, BlockTypeDomainSuffix:
		value = normalizeBlockDomain(value)
		if err := validateBlockDomain(value); err != nil {
			return BlockEntry{}, err
		}
	case BlockTypeDomainKeyword:
		if value == "" {
			return BlockEntry{}, fmt.Errorf("keyword is empty")
		}
	case BlockTypeGeosite:
		value = strings.ToLower(value)
		if !isGeositeName(value) {
			return BlockEntry{}, fmt.Errorf("invalid geosite category %q", value)
		}
	default:
		return BlockEntry{}, fmt.Errorf("unknown block type %q", e.Type)
	}
	return BlockEntry{Type: typ, Value: value, Remark: strings.TrimSpace(e.Remark), Enabled: e.Enabled}, nil
}

// detectBlockType picks ip-cidr for bare IPs / CIDRs, domain-suffix otherwise.
func detectBlockType(value string) string {
	if strings.Contains(value, "/") {
		if _, _, err := net.ParseCIDR(value); err == nil {
			return BlockTypeIPCIDR
		}
	}
	if net.ParseIP(value) != nil {
		return BlockTypeIPCIDR
	}
	return BlockTypeDomainSuffix
}

// looksLikeIPv4 reports whether value is four dot-separated all-numeric
// labels — the shape of an IPv4 literal, valid or not.
func looksLikeIPv4(value string) bool {
	labels := strings.Split(value, ".")
	if len(labels) != 4 {
		return false
	}
	for _, l := range labels {
		if l == "" || len(l) > 3 {
			return false
		}
		for _, r := range l {
			if r < '0' || r > '9' {
				return false
			}
		}
	}
	return true
}

// normalizeCIDR turns a bare IP into an explicit host route.
func normalizeCIDR(value string) string {
	if !strings.Contains(value, "/") {
		if strings.Contains(value, ":") {
			return value + "/128"
		}
		return value + "/32"
	}
	return value
}

// normalizeBlockDomain strips wildcard / leading-dot decoration so
// "*.example.com", ".example.com" and "example.com" are one suffix rule.
func normalizeBlockDomain(value string) string {
	value = strings.TrimPrefix(value, "*.")
	value = strings.TrimPrefix(value, ".")
	return strings.ToLower(value)
}

// validateBlockDomain accepts a hostname or a geosite-style dotted name.
func validateBlockDomain(value string) error {
	if value == "" || len(value) > maxBlocklistValueLen {
		return fmt.Errorf("invalid domain %q", value)
	}
	for _, label := range strings.Split(value, ".") {
		if label == "" {
			return fmt.Errorf("invalid domain %q (empty label)", value)
		}
		// RFC 1123 labels must not start or end with a hyphen; mihomo would
		// accept it, but such an entry can never match a real host.
		if strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return fmt.Errorf("invalid domain %q (label %q starts/ends with '-')", value, label)
		}
		for _, r := range label {
			switch {
			case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_':
			default:
				return fmt.Errorf("invalid character %q in domain %q", string(r), value)
			}
		}
	}
	return nil
}

func isGeositeName(value string) bool {
	if value == "" || len(value) > 64 {
		return false
	}
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_', r == '@':
		default:
			return false
		}
	}
	return true
}

// parseBlockLine parses one batch-import line. Supported forms:
//
//	example.com            → domain-suffix
//	full:example.com       → domain (exact)
//	keyword:cdn            → domain-keyword
//	geosite:google         → geosite
//	ip:1.2.3.4             → ip-cidr
//	10.0.0.0/8             → ip-cidr
//	anything # remark      → remark after '#'
func parseBlockLine(line string) (BlockEntry, error) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return BlockEntry{}, nil
	}
	remark := ""
	if i := strings.IndexByte(line, '#'); i >= 0 {
		remark = strings.TrimSpace(line[i+1:])
		line = strings.TrimSpace(line[:i])
	}
	typ := ""
	if prefix, payload, ok := strings.Cut(line, ":"); ok {
		switch strings.ToLower(strings.TrimSpace(prefix)) {
		case "full":
			typ, line = BlockTypeDomain, strings.TrimSpace(payload)
		case "suffix":
			typ, line = BlockTypeDomainSuffix, strings.TrimSpace(payload)
		case "keyword":
			typ, line = BlockTypeDomainKeyword, strings.TrimSpace(payload)
		case "geosite":
			typ, line = BlockTypeGeosite, strings.TrimSpace(payload)
		case "ip":
			typ, line = BlockTypeIPCIDR, strings.TrimSpace(payload)
		}
	}
	norm, err := normalizeBlockEntry(BlockEntry{Type: typ, Value: line, Remark: remark, Enabled: true})
	if err != nil {
		return BlockEntry{}, err
	}
	return norm, nil
}

// ParseBlocklistText parses a batch-import blob into normalized entries.
func ParseBlocklistText(text string) ([]BlockEntry, error) {
	out := []BlockEntry{}
	for _, line := range strings.Split(text, "\n") {
		e, err := parseBlockLine(line)
		if err != nil {
			return nil, err
		}
		if e.Value != "" {
			out = append(out, e)
		}
	}
	return NormalizeBlocklist(out)
}

// BlocklistParseResult is the detailed outcome of a batch import: the
// normalized entries ready to append plus per-line diagnostics, so the UI
// never has to re-implement the parsing rules.
type BlocklistParseResult struct {
	Entries    []BlockEntry `json:"entries"`
	Issues     []string     `json:"issues"`
	Duplicates int          `json:"duplicates"`
	Truncated  int          `json:"truncated"`
}

// ParseBlocklistTextDetailed parses a batch-import blob without aborting
// on the first bad line: every rejected line is reported with its 1-based
// line number. Duplicates inside the batch are dropped (first wins) and
// the result is capped at maxBlocklistEntries.
func ParseBlocklistTextDetailed(text string) BlocklistParseResult {
	// Non-nil slices keep the JSON contract [] (never null) for the UI.
	res := BlocklistParseResult{Entries: []BlockEntry{}, Issues: []string{}}
	var out []BlockEntry
	for i, line := range strings.Split(text, "\n") {
		e, err := parseBlockLine(line)
		if err != nil {
			res.Issues = append(res.Issues, fmt.Sprintf("line %d: %s", i+1, err.Error()))
			continue
		}
		if e.Value != "" {
			out = append(out, e)
		}
	}
	normalized, err := NormalizeBlocklist(out)
	if err != nil {
		// NormalizeBlocklist only fails on the cap or a validator miss;
		// fall back to entry-by-entry normalization for diagnostics.
		res.Issues = append(res.Issues, err.Error())
		normalized = []BlockEntry{}
		for i, e := range out {
			norm, nerr := normalizeBlockEntry(e)
			if nerr != nil {
				res.Issues = append(res.Issues, fmt.Sprintf("entry #%d: %s", i+1, nerr.Error()))
				continue
			}
			normalized = append(normalized, norm)
		}
	}
	res.Duplicates = len(out) - len(normalized)
	if len(normalized) > maxBlocklistEntries {
		res.Truncated = len(normalized) - maxBlocklistEntries
		normalized = normalized[:maxBlocklistEntries]
	}
	res.Entries = normalized
	return res
}

// blockRule renders one entry as a Mihomo rule line targeting REJECT.
// IP rules deliberately carry NO no-resolve: without it the engine resolves
// a domain destination at match time and compares the result, so the entry
// blocks clients that dial by name as well as clients that dial by IP.
// (With no-resolve only the latter ever matches — verified on a live node.)
func blockRule(e BlockEntry) string {
	switch e.Type {
	case BlockTypeIPCIDR:
		return "IP-CIDR," + e.Value + ",REJECT"
	case BlockTypeDomain:
		return "DOMAIN," + e.Value + ",REJECT"
	case BlockTypeDomainKeyword:
		return "DOMAIN-KEYWORD," + e.Value + ",REJECT"
	case BlockTypeGeosite:
		return "GEOSITE," + e.Value + ",REJECT"
	default:
		return "DOMAIN-SUFFIX," + e.Value + ",REJECT"
	}
}

// BlocklistRules renders the enabled entries as rule lines, preserving order.
func BlocklistRules(entries []BlockEntry) []string {
	rules := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.Enabled || e.Value == "" {
			continue
		}
		rules = append(rules, blockRule(e))
	}
	return rules
}

// blocklistHasDomainRules reports whether any enabled entry matches on a
// hostname (i.e. needs the sniffer to see TLS SNI / HTTP Host).
func blocklistHasDomainRules(entries []BlockEntry) bool {
	for _, e := range entries {
		if !e.Enabled || e.Value == "" {
			continue
		}
		switch e.Type {
		case BlockTypeDomain, BlockTypeDomainSuffix, BlockTypeDomainKeyword, BlockTypeGeosite:
			return true
		}
	}
	return false
}

// applyBlocklist prepends the blocklist REJECT rules to the serving config's
// rules — ahead of every other rule (WARP control-plane DIRECT included) so a
// blocked destination can never be proxied.
//
// Two enforcement paths, both verified against a live serving node:
//   - Hostname rules (DOMAIN/DOMAIN-SUFFIX/DOMAIN-KEYWORD/GEOSITE) match the
//     name the client sent or the sniffer recovered from TLS SNI / HTTP Host,
//     so the sniffer is armed for them (mirroring the WARP routing path).
//   - IP-CIDR rules are rendered without no-resolve, so the engine resolves a
//     domain destination at match time and compares the result; that covers
//     clients dialing by name as well as clients dialing by IP.
//
// DNS is therefore enabled whenever any rule is active.
func applyBlocklist(merged map[string]interface{}, entries []BlockEntry) {
	rules := BlocklistRules(entries)
	if len(rules) == 0 {
		return
	}
	raw, _ := merged["rules"].([]interface{})
	out := make([]interface{}, 0, len(rules)+len(raw))
	for _, r := range rules {
		out = append(out, r)
	}
	merged["rules"] = append(out, raw...)

	// Rules are only evaluated in rule mode; a global-mode fragment would
	// silently bypass the whole blocklist.
	merged["mode"] = "rule"
	if blocklistHasDomainRules(entries) {
		// Always refresh so a stale fragment cannot disable sniffing while
		// the blocklist is active.
		merged["sniffer"] = map[string]interface{}{
			"enable":               true,
			"parse-pure-ip":        true,
			"force-dns-mapping":    true,
			"override-destination": true,
			"sniff": map[string]interface{}{
				"TLS":  map[string]interface{}{"ports": []interface{}{443, 8443}},
				"HTTP": map[string]interface{}{"ports": []interface{}{80, "8080-8880"}},
			},
		}
	}
	dns, _ := merged["dns"].(map[string]interface{})
	if dns == nil {
		dns = map[string]interface{}{}
	}
	dns["enable"] = true
	if dns["nameserver"] == nil {
		dns["nameserver"] = []interface{}{"1.1.1.1", "8.8.8.8"}
	}
	if dns["enhanced-mode"] == nil || dns["enhanced-mode"] == "" {
		dns["enhanced-mode"] = "redir-host"
	}
	merged["dns"] = dns
}
