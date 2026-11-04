package static

import (
	"context"
	"errors"
	"strings"
	"testing"
)

const spec = " agent-token-0123456789=agent-troubleshoot:predict+read:162112+777 , human-token-0123456789 = gary , bare-token-0123456789=eval-grader::162112"

func TestParseAndVerify(t *testing.T) {
	v, err := Parse(spec)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	c, err := v.Verify(ctx, "agent-token-0123456789")
	if err != nil {
		t.Fatal(err)
	}
	if c.Subject != "agent-troubleshoot" || c.Issuer != "static" || !c.HasScope("predict") || !c.HasScope("read") || !c.MayAccessNetwork("777") || c.MayAccessNetwork("1") {
		t.Errorf("unexpected caller: %+v", c)
	}
	h, _ := v.Verify(ctx, "human-token-0123456789")
	if h.Subject != "gary" || len(h.Scopes) != 0 || !h.MayAccessNetwork("anything") {
		t.Errorf("unexpected caller: %+v", h)
	}
	g, _ := v.Verify(ctx, "bare-token-0123456789")
	if g.Subject != "eval-grader" || len(g.Scopes) != 0 || !g.MayAccessNetwork("162112") || g.MayAccessNetwork("1") {
		t.Errorf("unexpected caller: %+v", g)
	}
	if _, err := v.Verify(ctx, "nope"); !errors.Is(err, ErrUnknownToken) {
		t.Errorf("expected ErrUnknownToken, got %v", err)
	}
	if _, err := v.Verify(ctx, "agent-token-012345678"); !errors.Is(err, ErrUnknownToken) {
		t.Error("prefix of a token must not verify")
	}
	if got := strings.Join(v.Subjects(), ","); got != "agent-troubleshoot,gary,eval-grader" {
		t.Errorf("subjects: %s", got)
	}
}

func TestVerifyReturnsCopies(t *testing.T) {
	v, _ := Parse("agent-token-0123456789=a:predict")
	c1, _ := v.Verify(context.Background(), "agent-token-0123456789")
	c1.Scopes[0] = "tampered"
	c2, _ := v.Verify(context.Background(), "agent-token-0123456789")
	if c2.Scopes[0] != "predict" {
		t.Error("verifier state was mutated through a returned caller")
	}
}

func TestParseRejectsBadSpecs(t *testing.T) {
	for name, bad := range map[string]string{
		"empty":         "",
		"no equals":     "agent-token-0123456789",
		"short token":   "short=agent",
		"empty subject": "agent-token-0123456789=",
		"dup subject":   "agent-token-0123456789=a,other-token-0123456789=a",
		"dup token":     "agent-token-0123456789=a,agent-token-0123456789=b",
		"subject blank": "agent-token-0123456789= :predict",
		"only commas":   ", ,",
	} {
		if _, err := Parse(bad); err == nil {
			t.Errorf("%s: expected error for %q", name, bad)
		}
	}
}
