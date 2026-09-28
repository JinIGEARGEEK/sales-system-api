package apitests

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/igeargeek/sales-system-api/internal/config"
	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/testutil"
)

// loginLimit mirrors routes.go's loginLimiter Max.
const loginLimit = 10

// failedLogin sends one wrong-password login with the given X-Forwarded-For
// ("" for none) and returns the status.
func failedLogin(t *testing.T, app *fiber.App, xff string) int {
	t.Helper()
	req := testutil.NewRequest(t, http.MethodPost, "/api/v1/auth/login", map[string]interface{}{
		"email":    "nobody@igeargeek.com",
		"password": "wrong-password",
	}, "")
	if xff != "" {
		req.Header.Set("X-Forwarded-For", xff)
	}
	resp, err := app.Test(req, -1)
	require.NoError(t, err)
	resp.Body.Close()
	return resp.StatusCode
}

// TestLogin_RateLimited guards the new IP-based rate limit on POST
// /auth/login — previously unlimited, making it brute-forceable indefinitely.
func TestLogin_RateLimited(t *testing.T) {
	app, db := testutil.App(t)
	testutil.CreateUser(t, db, models.RoleAdmin) // just needs the table non-empty; login attempts below all fail on purpose

	var lastStatus int
	for i := 0; i < loginLimit+5; i++ {
		if lastStatus = failedLogin(t, app, ""); lastStatus == http.StatusTooManyRequests {
			break
		}
	}
	assert.Equal(t, http.StatusTooManyRequests, lastStatus, "expected the login limiter to eventually kick in")
}

// With no trusted proxy (the default off Railway), X-Forwarded-For is
// ignored: rotating it per attempt used to give every attempt a fresh
// bucket, i.e. no limit at all.
func TestLogin_RateLimit_RotatingXFFFromUntrustedPeerDoesNotBypass(t *testing.T) {
	app, db := testutil.App(t)
	testutil.CreateUser(t, db, models.RoleAdmin)

	var lastStatus int
	for i := 0; i < loginLimit+5; i++ {
		if lastStatus = failedLogin(t, app, fmt.Sprintf("198.51.100.%d", i)); lastStatus == http.StatusTooManyRequests {
			break
		}
	}
	assert.Equal(t, http.StatusTooManyRequests, lastStatus)
}

// Behind a trusted proxy (app.Test's fake peer is 0.0.0.0) each real client
// gets its own bucket, keyed on the hop the proxy saw — a client-prepended
// entry to its left changes nothing.
func TestLogin_RateLimit_PerClientBehindTrustedProxy(t *testing.T) {
	app, db := testutil.AppWithConfig(t, func(c *config.Config) { c.TrustedProxies = []string{"0.0.0.0"} })
	testutil.CreateUser(t, db, models.RoleAdmin)

	for i := 0; i < loginLimit; i++ {
		// Spoofed leftmost varies, the real client (rightmost) doesn't.
		require.NotEqual(t, http.StatusTooManyRequests, failedLogin(t, app, fmt.Sprintf("198.51.100.%d, 203.0.113.7", i)))
	}
	assert.Equal(t, http.StatusTooManyRequests, failedLogin(t, app, "1.1.1.1, 203.0.113.7"), "same real client is limited despite the spoofed prefix")
	assert.NotEqual(t, http.StatusTooManyRequests, failedLogin(t, app, "203.0.113.8"), "a different client has its own bucket")
}
