package ports

import "testing"

func TestBasicBearerRoundTrip(t *testing.T) {
	for _, c := range []struct{ key, secret string }{{"fwd-key-1", "s3cr3t"}, {"k", "with:colon"}, {"k", ""}} {
		b := BasicBearer(c.key, c.secret)
		k, s, ok := ParseBasicBearer(b)
		if !ok || k != c.key || s != c.secret {
			t.Errorf("%q/%q -> %q -> %q/%q %v", c.key, c.secret, b, k, s, ok)
		}
	}
	for _, bad := range []string{"", "eyJhbGciOi.x.y", "fmcp-basic.", "fmcp-basic.!!!", "fmcp-basic." + "bm9jb2xvbg"} { // last: "nocolon"
		if _, _, ok := ParseBasicBearer(bad); ok {
			t.Errorf("accepted %q", bad)
		}
	}
}
