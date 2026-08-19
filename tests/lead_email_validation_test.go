package apitests

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/testutil"
)

// TestLeadCreate_RejectsInvalidEmail guards the new email-format check —
// previously Lead.Email had no validation at all, and ImportContacts relies
// on Contact.Email as an exact-match dedupe key, so a garbage address would
// silently persist and then produce a silently-wrong import dedupe.
func TestLeadCreate_RejectsInvalidEmail(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)

	req := testutil.AuthRequest(t, http.MethodPost, "/api/v1/leads", map[string]interface{}{
		"name":  "Bad Email Lead",
		"email": "not-an-email",
	}, admin.ID, admin.Role)
	resp := doJSON(t, app, req, nil)
	assert.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)
}

// TestLeadCreate_AllowsEmptyEmail confirms the new check doesn't make email
// required — Lead.Email stays optional per api-system-spec.md §3.
func TestLeadCreate_AllowsEmptyEmail(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)

	req := testutil.AuthRequest(t, http.MethodPost, "/api/v1/leads", map[string]interface{}{
		"name": "No Email Lead",
	}, admin.ID, admin.Role)
	resp := doJSON(t, app, req, nil)
	assert.Equal(t, http.StatusCreated, resp.StatusCode)
}

// TestLeadCreate_AcceptsValidEmail is the positive-path sibling of the two
// tests above.
func TestLeadCreate_AcceptsValidEmail(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)

	req := testutil.AuthRequest(t, http.MethodPost, "/api/v1/leads", map[string]interface{}{
		"name":  "Good Email Lead",
		"email": "jane@example.com",
	}, admin.ID, admin.Role)
	resp := doJSON(t, app, req, nil)
	assert.Equal(t, http.StatusCreated, resp.StatusCode)
}
