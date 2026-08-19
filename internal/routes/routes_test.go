package routes

import (
	"io"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"
)

// TestClientIP guards the Railway-proxy IP resolution used to key the login
// rate limiter — c.IP() alone would return Railway's edge address for every
// request, collapsing all users onto one shared limiter bucket instead of
// limiting each caller independently.
func TestClientIP(t *testing.T) {
	app := fiber.New()
	app.Get("/", func(c *fiber.Ctx) error {
		return c.SendString(clientIP(c))
	})

	cases := []struct {
		name   string
		header string
		want   string
	}{
		{"no header falls back to RemoteAddr-derived c.IP()", "", "0.0.0.0"},
		{"single hop", "203.0.113.7", "203.0.113.7"},
		{"multi-hop chain takes the leftmost (original client)", "203.0.113.7, 10.0.0.5, 10.0.0.6", "203.0.113.7"},
		{"trims whitespace around the leftmost hop", "  203.0.113.7  , 10.0.0.5", "203.0.113.7"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/", nil)
			if tc.header != "" {
				req.Header.Set("X-Forwarded-For", tc.header)
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
			if got := string(body); got != tc.want {
				t.Errorf("clientIP with X-Forwarded-For %q = %q, want %q", tc.header, got, tc.want)
			}
		})
	}
}
