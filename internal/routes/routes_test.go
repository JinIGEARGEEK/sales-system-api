package routes

import (
	"io"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"

	"github.com/igeargeek/sales-system-api/internal/config"
)

// TestClientIP checks the login limiter's key function honors
// cfg.TrustedProxies — the resolution rules themselves are covered in
// internal/clientip. app.Test's fake connection reports its peer as 0.0.0.0.
func TestClientIP(t *testing.T) {
	cases := []struct {
		name    string
		trusted []string
		want    string
	}{
		{"no trusted proxy keys on the socket peer", nil, "0.0.0.0"},
		{"trusted peer keys on the rightmost untrusted hop", []string{"0.0.0.0"}, "203.0.113.7"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			key := clientIP(&config.Config{TrustedProxies: tc.trusted})
			app := fiber.New()
			app.Get("/", func(c *fiber.Ctx) error { return c.SendString(key(c)) })
			req := httptest.NewRequest("GET", "/", nil)
			req.Header.Set("X-Forwarded-For", "1.2.3.4, 203.0.113.7")
			resp, err := app.Test(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatal(err)
			}
			if got := string(body); got != tc.want {
				t.Errorf("clientIP = %q, want %q", got, tc.want)
			}
		})
	}
}
