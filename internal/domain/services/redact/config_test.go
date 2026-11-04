package redact

import (
	"encoding/json"
	"strings"
	"testing"
)

// Planted secrets: every one of these must be gone after Config().
var plantedSecrets = []string{
	"S3cr3tEnable", "$1$mERr$hx5rVt7rPNoS4wqbXKX7m0", "070C285F4D06", "0822455D0A16",
	"pubC0mm", "privRW-c0mm", "trapC0mm", "AuthK3y!v3", "PrivK3y!v3",
	"TacKey123", "RadKey456", "bgpNbrPw", "isakmpPSK99", "ospfAuthK", "md5DigestK",
	"ntpMd5Key", "keyStr1ng", "wpaPassphrase",
	"$6$saltsalt$abcdefghijklmnopqrstuvwxyz0123456789", "$9$dkrl8XN-wYgJDm5QF/9O1R", "$5$rounds$hashhash",
	"JunosPub", "j-psk-text", "junosNbrPw",
	"PanAdm1nPw", "panPhashValue", "panSnmpComm",
	"MIIEvQIBADANBgkqhkiG9w0BAQEFAASCBKcwggSjAgEAAoIBAQ",
}

// Operational data the agent needs: none of these may be redacted.
var mustSurvive = []string{
	"interface Ethernet1", "10.0.0.1 255.255.255.0", "neighbor 192.0.2.2 remote-as 65002",
	"set community 65000:100", "community-list standard CUST permit 65000:200",
	"router bgp 65001", "hostname core-1", "spanning-tree mode mstp",
	"key chain OSPF-KEYS", "logging host 10.9.9.9", "members 65000:300",
	"username admin privilege 15", "snmp-server location DC1",
	"ip address 10.1.1.1/24",
}

const ciscoIOS = `hostname core-1
enable secret 5 $1$mERr$hx5rVt7rPNoS4wqbXKX7m0
enable password 7 070C285F4D06
username admin privilege 15 secret 9 $9$dkrl8XN-wYgJDm5QF/9O1R
line vty 0 4
 password 7 0822455D0A16
snmp-server community pubC0mm RO
snmp-server community privRW-c0mm RW 10
snmp-server location DC1
snmp-server host 10.9.9.8 version 2c trapC0mm
snmp-server user mon MONGRP v3 auth sha AuthK3y!v3 priv aes 128 PrivK3y!v3
tacacs-server host 10.5.5.5 key 7 TacKey123
radius server R1
 key 7 RadKey456
interface Ethernet1
 ip address 10.0.0.1 255.255.255.0
 ip ospf authentication-key 7 ospfAuthK
 ip ospf message-digest-key 1 md5 7 md5DigestK
router bgp 65001
 neighbor 192.0.2.2 remote-as 65002
 neighbor 192.0.2.2 password 7 bgpNbrPw
crypto isakmp key isakmpPSK99 address 203.0.113.9
ntp authentication-key 1 md5 ntpMd5Key 7
key chain OSPF-KEYS
 key 1
  key-string 7 keyStr1ng
dot11 ssid CORP
 wpa-psk ascii 0 wpaPassphrase
route-map OUT permit 10
 set community 65000:100
ip community-list standard CUST permit 65000:200
spanning-tree mode mstp
logging host 10.9.9.9
`

const aristaEOS = `username admin privilege 15 role network-admin secret sha512 $6$saltsalt$abcdefghijklmnopqrstuvwxyz0123456789
interface Ethernet2
   ip address 10.1.1.1/24
`

const junos = `system {
    root-authentication {
        encrypted-password "$5$rounds$hashhash";
    }
}
snmp {
    community JunosPub {
        authorization read-only;
    }
}
security {
    ike {
        policy P1 {
            pre-shared-key ascii-text "j-psk-text";
        }
    }
}
protocols {
    bgp {
        group EXT {
            authentication-key "junosNbrPw";
        }
    }
}
policy-options {
    community CUST members 65000:300;
}
`

const panos = `<users><entry name="admin"><phash>panPhashValue</phash><password>PanAdm1nPw</password></entry></users>
<snmp-setting><access-setting><version><v2c><snmp-community-string>panSnmpComm</snmp-community-string></v2c></version></access-setting></snmp-setting>
`

const pemKey = "-----BEGIN PRIVATE KEY-----\nMIIEvQIBADANBgkqhkiG9w0BAQEFAASCBKcwggSjAgEAAoIBAQ\nabc\n-----END PRIVATE KEY-----\n"

func TestConfigRedactsPlantedSecretsAndKeepsOperationalData(t *testing.T) {
	all := ciscoIOS + aristaEOS + junos + panos + pemKey
	out, n := Config(all)
	for _, s := range plantedSecrets {
		if strings.Contains(out, s) {
			t.Errorf("secret survived: %q", s)
		}
	}
	for _, s := range mustSurvive {
		if !strings.Contains(out, s) {
			t.Errorf("operational data lost: %q", s)
		}
	}
	if n < len(plantedSecrets)-2 { // a few secrets share one match
		t.Errorf("redaction count %d looks too low for %d planted secrets", n, len(plantedSecrets))
	}
	if !strings.Contains(out, "<redacted:snmp-community>") || !strings.Contains(out, "<redacted:password>") {
		t.Errorf("placeholders should name the kind:\n%s", out)
	}
}

// Tool results are usually JSON: configs arrive as escaped strings with
// literal \n sequences. Redaction must work there and keep the JSON valid.
func TestConfigInsideJSONStaysValid(t *testing.T) {
	payload := map[string]any{
		"device": "core-1",
		"config": ciscoIOS + junos,
		"lines":  strings.Split(aristaEOS, "\n"),
	}
	raw, _ := json.Marshal(payload)
	text := "Config search completed:\n" + string(raw) + "\nEvidence: {\"kind\":\"nqe\",\"ref\":\"nqe:FQ_x\",\"snapshot_id\":\"656\"}\n"
	out, _ := Config(text)
	for _, s := range plantedSecrets[:len(plantedSecrets)-4] { // the PAN-OS/PEM ones are not in this payload
		if strings.Contains(out, s) {
			t.Errorf("secret survived in JSON: %q", s)
		}
	}
	start, end := strings.Index(out, "{"), strings.Index(out, "\nEvidence:")
	var back map[string]any
	if err := json.Unmarshal([]byte(out[start:end]), &back); err != nil {
		t.Fatalf("redaction broke the JSON: %v\n%s", err, out[start:end])
	}
	if !strings.Contains(out, `"snapshot_id":"656"`) || !strings.Contains(back["config"].(string), "set community 65000:100") {
		t.Error("evidence line or operational data damaged")
	}
}

func TestConfigIsIdempotentAndCheapOnCleanText(t *testing.T) {
	once, n1 := Config(ciscoIOS)
	twice, n2 := Config(once)
	if once != twice || n2 != 0 || n1 == 0 {
		t.Fatalf("not idempotent: n1=%d n2=%d", n1, n2)
	}
	clean := `{"rows":[{"device":"leaf-1","interface":"Ethernet3","operStatus":"DOWN"}]}`
	if out, n := Config(clean); out != clean || n != 0 {
		t.Fatalf("clean text changed: %q", out)
	}
}

func TestConfigRedactorSatisfiesPortShape(t *testing.T) {
	var r interface{ Redact(string) (string, int) } = ConfigRedactor{}
	if out, n := r.Redact("snmp-server community abc RO"); n != 1 || strings.Contains(out, "abc") {
		t.Fatalf("%q %d", out, n)
	}
}
