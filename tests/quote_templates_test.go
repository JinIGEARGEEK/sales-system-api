package apitests

import (
	"net/http"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/stretchr/testify/require"

	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/testutil"
)

// TestQuoteTemplate_CreateListDelete guards QuoteTemplateHandler's minimal
// Save/List/Delete scope (no Update — see models.QuoteTemplate's own doc).
func TestQuoteTemplate_CreateListDelete(t *testing.T) {
	app, db := testutil.App(t)
	rep := testutil.CreateUser(t, db, models.RoleSalesRep)

	createReq := testutil.AuthRequest(t, http.MethodPost, "/api/v1/quote-templates", map[string]interface{}{
		"name":          "Standard Package",
		"scope_of_work": "Website + hosting",
	}, rep.ID, rep.Role)
	var created struct {
		Data models.QuoteTemplate `json:"data"`
	}
	resp := doJSON(t, app, createReq, &created)
	require.Equal(t, fiber.StatusCreated, resp.StatusCode)
	require.Equal(t, "Standard Package", created.Data.Name)
	// price_type defaults when omitted.
	require.Equal(t, models.QuotePriceTypeExclTax, created.Data.PriceType)

	listReq := testutil.AuthRequest(t, http.MethodGet, "/api/v1/quote-templates", nil, rep.ID, rep.Role)
	var list struct {
		Data []models.QuoteTemplate `json:"data"`
	}
	listResp := doJSON(t, app, listReq, &list)
	require.Equal(t, fiber.StatusOK, listResp.StatusCode)
	require.Len(t, list.Data, 1)

	deleteReq := testutil.AuthRequest(t, http.MethodDelete, "/api/v1/quote-templates/"+itoa(created.Data.ID), nil, rep.ID, rep.Role)
	deleteResp := doJSON(t, app, deleteReq, nil)
	require.Equal(t, fiber.StatusNoContent, deleteResp.StatusCode)

	var count int64
	db.Model(&models.QuoteTemplate{}).Count(&count)
	require.Zero(t, count, "Delete is a hard delete — no trash/restore for QuoteTemplate")
}

// TestQuoteTemplate_CreateRequiresName guards the "name is required" 422.
func TestQuoteTemplate_CreateRequiresName(t *testing.T) {
	app, db := testutil.App(t)
	rep := testutil.CreateUser(t, db, models.RoleSalesRep)

	req := testutil.AuthRequest(t, http.MethodPost, "/api/v1/quote-templates", map[string]interface{}{}, rep.ID, rep.Role)
	resp := doJSON(t, app, req, nil)
	require.Equal(t, fiber.StatusUnprocessableEntity, resp.StatusCode)
}

// TestQuoteTemplate_DeleteNotFound guards the 404 path.
func TestQuoteTemplate_DeleteNotFound(t *testing.T) {
	app, db := testutil.App(t)
	rep := testutil.CreateUser(t, db, models.RoleSalesRep)

	req := testutil.AuthRequest(t, http.MethodDelete, "/api/v1/quote-templates/999999", nil, rep.ID, rep.Role)
	resp := doJSON(t, app, req, nil)
	require.Equal(t, fiber.StatusNotFound, resp.StatusCode)
}
