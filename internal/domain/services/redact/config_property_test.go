package redact

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

// Property: for every secret-bearing config template and any secret made of
// characters real devices accept, the secret never survives, in raw form or
// inside a JSON string. Independent of the hand-picked fixtures above.

var secretTemplates = []string{
	"enable secret 5 %s", "enable password 7 %s", "username u privilege 15 secret 9 %s",
	"username u role network-admin secret sha512 %s", " password 7 %s", " password %s",
	"snmp-server community %s RO", "snmp-server community %s", "snmp-server host 10.0.0.9 version 2c %s",
	"snmp-server host 10.0.0.9 traps version 2c %s udp-port 162",
	"snmp-server user m G v3 auth sha %s priv aes 128 x", "snmp-server user m G v3 auth md5 x priv des %s",
	"tacacs-server host 10.0.0.5 key 7 %s", "tacacs-server key %s", "radius-server host 10.0.0.6 key %s",
	" key 7 %s", " ip ospf authentication-key %s", " ip ospf message-digest-key 3 md5 %s",
	"ntp authentication-key 2 md5 %s 7", "  key-string %s", "crypto isakmp key %s address 0.0.0.0",
	" pre-shared-key address 1.2.3.4 key %s", " wpa-psk ascii 0 %s", " neighbor 10.0.0.2 password 7 %s",
	`        encrypted-password "%s";`, `            pre-shared-key ascii-text "%s";`,
	`            authentication-key "%s";`, "    community %s {", "set snmp community %s authorization read-only",
	"<password>%s</password>", "<phash>%s</phash>", "<snmp-community-string>%s</snmp-community-string>",
}

const secretAlphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789!@#%^&*()-_=+[]|:./?~$,"

func randomSecret(r *rand.Rand) string {
	n := 6 + r.Intn(20)
	b := make([]byte, n)
	for i := range b {
		b[i] = secretAlphabet[r.Intn(len(secretAlphabet))]
	}
	// Prefix a marker that cannot occur in the surrounding template, so a
	// partial survival (e.g. a tail after a stop character) is detectable.
	return "Zq" + string(b)
}

func TestPropertyNoSecretSurvives(t *testing.T) {
	r := rand.New(rand.NewSource(20261104))
	for i := 0; i < 1000; i++ {
		tmpl := secretTemplates[r.Intn(len(secretTemplates))]
		secret := randomSecret(r)
		line := fmt.Sprintf(tmpl, secret)
		doc := "hostname h\n" + line + "\ninterface Ethernet1\n"

		out, _ := Config(doc)
		if strings.Contains(out, secret[:6]) {
			t.Fatalf("raw: secret %q survived template %q:\n%s", secret, tmpl, out)
		}
		raw, _ := json.Marshal(map[string]string{"config": doc})
		jout, _ := Config(string(raw))
		if strings.Contains(jout, secret[:6]) {
			t.Fatalf("json: secret %q survived template %q:\n%s", secret, tmpl, jout)
		}
		var back map[string]string
		if err := json.Unmarshal([]byte(jout), &back); err != nil {
			t.Fatalf("json broken for template %q secret %q: %v\n%s", tmpl, secret, err, jout)
		}
	}
}

// Property: lines with no secret are returned byte-for-byte.
func TestPropertyBenignLinesUntouched(t *testing.T) {
	benign := []string{
		"interface Ethernet%d", " ip address 10.%d.0.1 255.255.255.0", "router bgp 650%02d",
		" neighbor 192.0.2.%d remote-as 65002", " set community 65000:%d additive",
		"ip community-list standard C%d permit 65000:1", "vlan %d", " description uplink-%d",
		"key chain K%d", " key %d", "ntp server 10.0.0.%d", "logging host 10.9.9.%d",
		"policy-options community C%d members 65000:300;", "snmp-server location rack-%d",
		"snmp-server enable traps bgp %d", " spanning-tree portfast %d",
	}
	r := rand.New(rand.NewSource(7))
	for i := 0; i < 500; i++ {
		line := fmt.Sprintf(benign[r.Intn(len(benign))], r.Intn(250))
		if out, n := Config(line); out != line || n != 0 {
			t.Fatalf("benign line changed: %q -> %q", line, out)
		}
	}
}
