package redact

import (
	"regexp"
	"strings"
)

// Config removes secrets that live inside network device configuration
// (and other tool output) before it reaches a model, a trace or a
// recording. Values are replaced with "<redacted:KIND>" so an agent can
// still reason that, say, an SNMP community is configured without seeing
// it. It returns the redacted text and the number of replacements.
//
// It works on raw config and on config embedded in JSON strings, where
// line breaks are the two characters `\n` and quotes are `\"`: values
// never extend across a backslash, quote or whitespace, so JSON stays
// valid. Separators between keywords are spaces or tabs only, so a
// pattern never jumps to the next config line.
//
// Patterns are deliberately broad on secrets and narrow on operational
// data: BGP communities, interface names, addresses and key-chain names
// survive (see config_test.go for both lists).
func Config(s string) (string, int) {
	if s == "" {
		return s, 0
	}
	total := 0
	for _, r := range configRules {
		var n int
		s, n = r.apply(s)
		total += n
	}
	// Then the generic credential shapes (JWTs, bearer values, key=value).
	if out := String(s); out != s {
		total += countDiff(s, out)
		s = out
	}
	return s, total
}

// ConfigRedactor adapts Config to the ports.OutputRedactor shape.
type ConfigRedactor struct{}

// Redact implements ports.OutputRedactor.
func (ConfigRedactor) Redact(s string) (string, int) { return Config(s) }

type rule struct {
	kind  string
	re    *regexp.Regexp
	whole bool // replace the whole match, not just the last group
}

// value is a secret token. A quoted value ("..." or JSON-escaped \"...\")
// runs to its closing quote. An unquoted value stops at whitespace, a
// quote, a lone backslash (the start of a JSON \n or \t), ';', '<', '>',
// '{' or '}', but may contain JSON escapes \uXXXX (Go escapes '&', '<' and
// '>' this way) and \\ so that a secret containing them is redacted whole.
// Exactly one of the two groups matches; apply takes the last one that did.
const value = `(?:\\?"((?:[^"\\]|\\u[0-9a-fA-F]{4}|\\\\)+)\\?"|((?:[^\s"\\;<>{}]|\\u[0-9a-fA-F]{4}|\\\\)+))`

// lt and gt match '<' and '>' raw or JSON-escaped.
const lt = `(?:<|\\u003[cC])`
const gt = `(?:>|\\u003[eE])`

// sp is in-line whitespace only.
const sp = `[ \t]+`

// lineStart is a real or JSON-escaped line start.
const lineStart = `(?:^|\n|\\n)[ \t]*`

// nonSpace is a token that does not cross a JSON escape.
const nonSpace = `[^ \t\n\\]+`

// wb is a word boundary that also holds right after a JSON-escaped line
// break or tab ("\nsnmp-server" has no \b before "snmp": 'n' is a word
// character).
const wb = `(?:\\[nt]|\b)`

func mk(kind, pattern string) rule { return rule{kind: kind, re: regexp.MustCompile(pattern)} }

var configRules = []rule{
	{kind: "private-key", whole: true, re: regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----[\s\S]*?-----END [A-Z ]*PRIVATE KEY-----`)},

	// SNMP
	mk("snmp-community", wb+`snmp-server`+sp+`community`+sp+value),
	mk("snmp-community", wb+`snmp-server`+sp+`host`+sp+nonSpace+sp+`(?:(?:traps|informs)`+sp+`)?(?:version`+sp+`(?:1|2c)`+sp+`)`+value),
	mk("snmp-community", wb+`snmp`+sp+`community`+sp+value),  // Junos set form
	mk("snmp-community", wb+`community`+sp+value+`[ \t]*\{`), // Junos hierarchy (policy communities have no brace)
	mk("snmp-key", wb+`auth`+sp+`(?:md5|sha[^ \t\n\\]*)`+sp+`(?:localized[^ \t\n\\]*`+sp+`)?`+value),
	mk("snmp-key", wb+`priv`+sp+`(?:des[^ \t\n\\]*|3des|aes(?:`+sp+`\d+)?)`+sp+`(?:localized[^ \t\n\\]*`+sp+`)?`+value),

	// AAA servers
	mk("aaa-key", wb+`(?:tacacs-server|radius-server)\b[^\n\\]*?\bkey`+sp+`(?:\d`+sp+`)?`+value),
	mk("aaa-key", lineStart+`(?:server-)?key`+sp+`\d`+sp+value), // "key 7 x" inside a server block

	// Routing protocol and NTP authentication
	mk("auth-key", wb+`message-digest-key`+sp+`\d+`+sp+`md5`+sp+`(?:\d`+sp+`)?`+value),
	mk("auth-key", wb+`authentication-key`+sp+`(?:\d+`+sp+`)?(?:md5`+sp+`)?(?:\d`+sp+`)?`+value),
	mk("auth-key", wb+`key-string`+sp+`(?:\d`+sp+`)?`+value),

	// VPN and wireless pre-shared keys
	mk("psk", wb+`isakmp`+sp+`key`+sp+`(?:\d`+sp+`)?`+value),
	mk("psk", wb+`pre-shared-key`+sp+`(?:(?:ascii-text|hexadecimal|local|remote|key|address`+sp+nonSpace+`)`+sp+`)*(?:\d`+sp+`)?`+value),
	mk("psk", wb+`wpa-psk`+sp+`(?:ascii|hex)`+sp+`(?:\d`+sp+`)?`+value),

	// Passwords and secrets (IOS, NX-OS, EOS, Junos)
	mk("password", wb+`(?:encrypted-password|password|secret)`+sp+`(?:(?:\d{1,2}|sha512|sha256|md5)`+sp+`)?`+value),

	// PAN-OS XML
	mk("snmp-community", lt+`(?:snmp-community-string|community)`+gt+`(.+?)`+lt+`/`),
	mk("password", lt+`(?:password|phash|key|secret|pre-shared-key|auth-password|priv-password)`+gt+`(.+?)`+lt+`/`),

	// Any crypt(3) hash left over ($1$ md5, $5$/$6$ sha, $8$/$9$ scrypt, $2a$ bcrypt).
	{kind: "hash", whole: true, re: regexp.MustCompile(`\$(?:1|2[aby]?|5|6|8|9)\$[^\s"\\,;<>{}]+`)},
}

func (r rule) apply(s string) (string, int) {
	matches := r.re.FindAllStringSubmatchIndex(s, -1)
	if len(matches) == 0 {
		return s, 0
	}
	var b strings.Builder
	last, n := 0, 0
	for _, m := range matches {
		start, end := m[0], m[1]
		if !r.whole {
			start, end = -1, -1
			for g := len(m)/2 - 1; g >= 1; g-- { // last group that matched
				if m[2*g] >= 0 {
					start, end = m[2*g], m[2*g+1]
					break
				}
			}
		}
		if start < 0 || start < last {
			continue
		}
		// An optional type token ("secret 5 ...", "aes 128 ...") must not
		// be taken for the secret when the secret is already redacted:
		// that keeps Config idempotent.
		if !r.whole && strings.HasPrefix(strings.TrimLeft(s[end:], " \t"), "<redacted:") {
			continue
		}
		b.WriteString(s[last:start])
		b.WriteString("<redacted:" + r.kind + ">")
		last = end
		n++
	}
	b.WriteString(s[last:])
	return b.String(), n
}

// countDiff approximates how many generic redactions String made.
func countDiff(before, after string) int {
	n := strings.Count(after, "[REDACTED]") - strings.Count(before, "[REDACTED]")
	if n < 1 {
		return 1
	}
	return n
}
