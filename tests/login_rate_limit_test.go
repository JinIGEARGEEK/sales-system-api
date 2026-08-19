package apitests

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/testutil"
)

// TestLogin_RateLimited guards the new IP-based rate limit on POST
// /auth/login — previously unlimited, making it brute-forceable indefinitely.
func TestLogin_RateLimited(t *testing.T) {
	app, db := testutil.App(t)
	testutil.CreateUser(t, db, models.RoleAdmin) // just needs the table non-empty; login attempts below all fail on purpose

	var lastStatus int
	for i := 0; i < 15; i++ {
		req := testutil.NewRequest(t, http.MethodPost, "/api/v1/auth/login", map[string]interface{}{
			"email":    "nobody@igeargeek.com",
			"password": "wrong-password",
		}, "")
		resp, err := app.Test(req, -1)
		require.NoError(t, err)
		lastStatus = resp.StatusCode
		resp.Body.Close()
		if lastStatus == http.StatusTooManyRequests {
			break
		}
	}
	assert.Equal(t, http.StatusTooManyRequests, lastStatus, "expected the login limiter to eventually kick in")
}
