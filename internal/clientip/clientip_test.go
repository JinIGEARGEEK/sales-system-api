package clientip

import (
	"io"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"
)

// resolve runs r.ClientIP for one request through app.Test, whose fake
// connection always reports the socket peer as 0.0.0.0 — so "0.0.0.0" in a
// trusted list below stands in for "the request came via our proxy".
func resolve(t *testing.T, r *Resolver, xff string) string {
	t.Helper()
	app := fiber.New()
	app.Get("/", func(c *fiber.Ctx) error { return c.SendString(r.ClientIP(c)) })
	req := httptest.NewRequest("GET", "/", nil)
	if xff != "" {
		req.Header.Set("X-Forwarded-For", xff)
	}
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func TestClientIP(t *testing.T) {
	cases := []struct {
		name    string
		trusted []string
		xff     string
		want    string
	}{
		// Untrusted peer: the header is ignored entirely, however it looks —
		// otherwise rotating it per request resets the login limiter.
		{"no trusted proxies ignores the header", nil, "203.0.113.7", "0.0.0.0"},
		{"untrusted peer ignores the header", []string{"10.0.0.0/8"}, "203.0.113.7", "0.0.0.0"},
		{"no header, trusted peer", []string{"0.0.0.0"}, "", "0.0.0.0"},

		// Trusted peer: rightmost untrusted hop wins.
		{"single hop", []string{"0.0.0.0"}, "203.0.113.7", "203.0.113.7"},
		{"client-prepended spoof is skipped", []string{"0.0.0.0"}, "1.2.3.4, 203.0.113.7", "203.0.113.7"},
		{"trusted inner hops are skipped", []string{"0.0.0.0", "10.0.0.0/8"}, "1.2.3.4, 203.0.113.7, 10.0.0.5, 10.1.2.3", "203.0.113.7"},
		{"whitespace is trimmed", []string{"0.0.0.0"}, "  203.0.113.7  ", "203.0.113.7"},
		{"garbage hop stops the walk at the proxy", []string{"0.0.0.0"}, "not-an-ip", "0.0.0.0"},
		{"garbage left of a real hop is never reached", []string{"0.0.0.0"}, "not-an-ip, 203.0.113.7", "203.0.113.7"},
		{"all hops trusted keys on the leftmost", []string{"0.0.0.0", "10.0.0.0/8"}, "10.0.0.9, 10.0.0.5", "10.0.0.9"},
		{"ipv6 client", []string{"0.0.0.0"}, "2001:db8::1", "2001:db8::1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, err := New(tc.trusted)
			if err != nil {
				t.Fatal(err)
			}
			if got := resolve(t, r, tc.xff); got != tc.want {
				t.Errorf("ClientIP(trusted=%v, X-Forwarded-For=%q) = %q, want %q", tc.trusted, tc.xff, got, tc.want)
			}
		})
	}
}

func TestNew_RejectsInvalidEntries(t *testing.T) {
	for _, bad := range []string{"10.0.0.0/33", "not-an-ip", "10.0.0"} {
		if _, err := New([]string{bad}); err == nil {
			t.Errorf("New(%q) = nil error, want one", bad)
		}
	}
	if _, err := New([]string{" 10.0.0.1 ", "", "fc00::/7"}); err != nil {
		t.Errorf("New(valid entries) = %v", err)
	}
}
