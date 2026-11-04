package config

import (
	"strings"
	"testing"
)

func TestForwardAuthValidation(t *testing.T) {
	ok := map[string]ForwardConfig{
		"basic default":  {APIKey: "k", APISecret: "s"},
		"basic explicit": {AuthType: "basic", APIKey: "k", APISecret: "s"},
		"token":          {AuthType: "token", APIToken: "tok-0123456789"},
		"jwt file":       {AuthType: "jwt", APIJWTFile: "/var/run/secrets/forward/jwt"},
		"jwt value":      {AuthType: "jwt", APIJWT: "eyJ.a.b"},
	}
	for name, c := range ok {
		if err := c.ValidateAuth(); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	bad := map[string]ForwardConfig{
		"basic without secret": {APIKey: "k"},
		"basic without key":    {AuthType: "basic", APISecret: "s"},
		"token without token":  {AuthType: "token", APIKey: "k", APISecret: "s"},
		"jwt without source":   {AuthType: "jwt"},
		"unknown type":         {AuthType: "kerberos", APIKey: "k", APISecret: "s"},
	}
	for name, c := range bad {
		err := c.ValidateAuth()
		if err == nil {
			t.Errorf("%s: accepted", name)
			continue
		}
		if strings.Contains(err.Error(), "s3cr3t") {
			t.Errorf("%s: error leaks a credential: %v", name, err)
		}
	}
	if (ForwardConfig{}).EffectiveAuthType() != "basic" || (ForwardConfig{AuthType: "TOKEN"}).EffectiveAuthType() != "token" {
		t.Error("auth type normalisation")
	}
}
