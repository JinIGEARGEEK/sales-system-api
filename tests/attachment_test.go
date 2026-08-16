package apitests

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/testutil"
)

// TestAttachmentCreate_RBAC guards the route-level gate on POST /attachments:
// Production must 403 (matches Project create RBAC), Sales Rep/Admin must succeed.
func TestAttachmentCreate_RBAC(t *testing.T) {
	app, db := testutil.App(t)
	company := seedCompany(t, db)
	production := testutil.CreateUser(t, db, models.RoleProduction)
	rep := testutil.CreateUser(t, db, models.RoleSalesRep)

	body := map[string]interface{}{
		"related_type": "company", "related_id": company.ID,
		"category": "Proposal", "file_name": "Proposal.pdf", "external_url": "https://docs.google.com/document/d/abc",
	}

	t.Run("forbidden for production", func(t *testing.T) {
		req := testutil.AuthRequest(t, http.MethodPost, "/api/v1/attachments", body, production.ID, production.Role)
		resp := doJSON(t, app, req, nil)
		assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	})

	t.Run("allowed for sales rep", func(t *testing.T) {
		req := testutil.AuthRequest(t, http.MethodPost, "/api/v1/attachments", body, rep.ID, rep.Role)
		resp := doJSON(t, app, req, nil)
		assert.Equal(t, http.StatusCreated, resp.StatusCode)
	})
}

// TestAttachmentCreate_ExactlyOneSource guards the file_url/external_url
// exclusivity: a request with neither must be rejected.
func TestAttachmentCreate_ExactlyOneSource(t *testing.T) {
	app, db := testutil.App(t)
	company := seedCompany(t, db)
	rep := testutil.CreateUser(t, db, models.RoleSalesRep)

	t.Run("missing external_url and file rejected", func(t *testing.T) {
		req := testutil.AuthRequest(t, http.MethodPost, "/api/v1/attachments", map[string]interface{}{
			"related_type": "company", "related_id": company.ID,
			"category": "Proposal", "file_name": "Proposal.pdf",
		}, rep.ID, rep.Role)
		resp := doJSON(t, app, req, nil)
		assert.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)
	})
}

// TestAttachmentDelete_Ownership mirrors TestActivityDelete_Ownership: a
// non-uploader Sales Rep must 403, the uploader or an Admin must succeed.
func TestAttachmentDelete_Ownership(t *testing.T) {
	app, db := testutil.App(t)
	company := seedCompany(t, db)
	repA := testutil.CreateUser(t, db, models.RoleSalesRep)
	repB := testutil.CreateUser(t, db, models.RoleSalesRep)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)

	createAttachment := func() *models.Attachment {
		req := testutil.AuthRequest(t, http.MethodPost, "/api/v1/attachments", map[string]interface{}{
			"related_type": "company", "related_id": company.ID,
			"category": "Support", "file_name": "Sheet.xlsx", "external_url": "https://docs.google.com/spreadsheets/d/abc",
		}, repA.ID, repA.Role)
		var respBody struct {
			Data models.Attachment `json:"data"`
		}
		resp := doJSON(t, app, req, &respBody)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("failed to seed attachment, status=%d", resp.StatusCode)
		}
		return &respBody.Data
	}

	t.Run("forbidden for non-uploader rep", func(t *testing.T) {
		attachment := createAttachment()
		req := testutil.AuthRequest(t, http.MethodDelete, "/api/v1/attachments/"+itoa(attachment.ID), nil, repB.ID, repB.Role)
		resp := doJSON(t, app, req, nil)
		assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	})

	t.Run("allowed for uploader", func(t *testing.T) {
		attachment := createAttachment()
		req := testutil.AuthRequest(t, http.MethodDelete, "/api/v1/attachments/"+itoa(attachment.ID), nil, repA.ID, repA.Role)
		resp := doJSON(t, app, req, nil)
		assert.Equal(t, http.StatusNoContent, resp.StatusCode)
	})

	t.Run("allowed for admin", func(t *testing.T) {
		attachment := createAttachment()
		req := testutil.AuthRequest(t, http.MethodDelete, "/api/v1/attachments/"+itoa(attachment.ID), nil, admin.ID, admin.Role)
		resp := doJSON(t, app, req, nil)
		assert.Equal(t, http.StatusNoContent, resp.StatusCode)
	})
}

// TestLeadConvert_CarriesAttachments guards FR-CRM-090: converting a Lead
// must re-point its Attachment rows onto the resulting Deal.
func TestLeadConvert_CarriesAttachments(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)

	lead := &models.Lead{Name: "Jordan Lee", CompanyName: "Acme Corp", Source: models.LeadSourceWebsite, Status: models.LeadStatusQualified}
	require.NoError(t, db.Create(lead).Error)

	createReq := testutil.AuthRequest(t, http.MethodPost, "/api/v1/attachments", map[string]interface{}{
		"related_type": "lead", "related_id": lead.ID,
		"category": "Estimation", "file_name": "Estimate.pdf", "external_url": "https://docs.google.com/document/d/xyz",
	}, admin.ID, admin.Role)
	var createBody struct {
		Data models.Attachment `json:"data"`
	}
	createResp := doJSON(t, app, createReq, &createBody)
	require.Equal(t, http.StatusCreated, createResp.StatusCode)

	convertReq := testutil.AuthRequest(t, http.MethodPost, "/api/v1/leads/"+itoa(lead.ID)+"/convert", map[string]interface{}{
		"deal": map[string]interface{}{"title": "Acme Deal", "value": 5000, "stage": "Lead"},
	}, admin.ID, admin.Role)
	var convertBody struct {
		Data struct {
			Deal models.Deal `json:"deal"`
		} `json:"data"`
	}
	convertResp := doJSON(t, app, convertReq, &convertBody)
	require.Equal(t, http.StatusOK, convertResp.StatusCode)

	var attachment models.Attachment
	require.NoError(t, db.First(&attachment, createBody.Data.ID).Error)
	assert.Equal(t, models.AttachmentRelatedDeal, attachment.RelatedType)
	assert.Equal(t, convertBody.Data.Deal.ID, attachment.RelatedID)
}
