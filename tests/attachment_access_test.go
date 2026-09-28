package apitests

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/testutil"
)

// seedLinkAttachment inserts an external-link Attachment on relatedType/
// relatedID directly, bypassing Create's access checks.
func seedLinkAttachment(t *testing.T, db *gorm.DB, relatedType models.AttachmentRelatedType, relatedID, uploaderID uint) *models.Attachment {
	t.Helper()
	link := "https://docs.google.com/document/d/seeded"
	a := &models.Attachment{
		RelatedType: relatedType, RelatedID: relatedID, Category: models.AttachmentCategoryProposal,
		FileName: "Seeded.pdf", ExternalURL: &link, UploadedByID: uploaderID,
	}
	require.NoError(t, db.Create(a).Error)
	return a
}

func attachmentLinkBody(relatedType models.AttachmentRelatedType, relatedID uint, externalURL string) map[string]interface{} {
	return map[string]interface{}{
		"related_type": relatedType, "related_id": relatedID,
		"category": "Proposal", "file_name": "Proposal.pdf", "external_url": externalURL,
	}
}

// TestAttachmentList_RequiresRelatedRecord — an unfiltered GET /attachments
// used to return every attachment in the system, file URLs included, to any
// authenticated role.
func TestAttachmentList_RequiresRelatedRecord(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	company := seedCompany(t, db)
	seedLinkAttachment(t, db, models.AttachmentRelatedCompany, company.ID, admin.ID)

	for _, query := range []string{"", "?related_type=company", "?related_id=" + itoa(company.ID), "?related_type=company&related_id=abc"} {
		req := testutil.AuthRequest(t, http.MethodGet, "/api/v1/attachments"+query, nil, admin.ID, admin.Role)
		assert.Equal(t, http.StatusUnprocessableEntity, doJSON(t, app, req, nil).StatusCode, query)
	}

	req := testutil.AuthRequest(t, http.MethodGet, "/api/v1/attachments?related_type=widget&related_id=1", nil, admin.ID, admin.Role)
	assert.Equal(t, http.StatusUnprocessableEntity, doJSON(t, app, req, nil).StatusCode, "unknown related_type")
}

// TestAttachmentList_ParentReadAccess — attachments on a Deal/Prospect/Quote
// follow those records' salesPipelineRoles gate (Production 403s), while
// Company attachments stay readable by every role, same as the Company.
func TestAttachmentList_ParentReadAccess(t *testing.T) {
	app, db := testutil.App(t)
	production := testutil.CreateUser(t, db, models.RoleProduction)
	rep := testutil.CreateUser(t, db, models.RoleSalesRep)

	deal := seedDeal(t, db, nil)
	quote := &models.Quote{DealID: deal.ID}
	require.NoError(t, db.Create(quote).Error)
	prospect := seedProspect(t, db, nil)
	company := seedCompany(t, db)
	for _, p := range []struct {
		typ models.AttachmentRelatedType
		id  uint
	}{
		{models.AttachmentRelatedDeal, deal.ID},
		{models.AttachmentRelatedQuote, quote.ID},
		{models.AttachmentRelatedProspect, prospect.ID},
		{models.AttachmentRelatedCompany, company.ID},
	} {
		seedLinkAttachment(t, db, p.typ, p.id, rep.ID)
	}

	cases := []struct {
		query     string
		user      *models.User
		want      int
		wantCount int
	}{
		{"related_type=deal&related_id=" + itoa(deal.ID), production, http.StatusForbidden, 0},
		{"related_type=quote&related_id=" + itoa(quote.ID), production, http.StatusForbidden, 0},
		{"related_type=prospect&related_id=" + itoa(prospect.ID), production, http.StatusForbidden, 0},
		{"related_type=company&related_id=" + itoa(company.ID), production, http.StatusOK, 1},
		{"related_type=deal&related_id=" + itoa(deal.ID), rep, http.StatusOK, 1},
		{"related_type=quote&related_id=" + itoa(quote.ID), rep, http.StatusOK, 1},
		{"related_type=deal&related_id=999999", rep, http.StatusNotFound, 0},
	}
	for _, tc := range cases {
		t.Run(string(tc.user.Role)+" "+tc.query, func(t *testing.T) {
			req := testutil.AuthRequest(t, http.MethodGet, "/api/v1/attachments?"+tc.query, nil, tc.user.ID, tc.user.Role)
			var body struct {
				Data []models.Attachment `json:"data"`
			}
			resp := doJSON(t, app, req, &body)
			assert.Equal(t, tc.want, resp.StatusCode)
			assert.Len(t, body.Data, tc.wantCount)
		})
	}
}

// TestAttachmentCreate_ParentMustExistAndBeWritable — attaching to a record
// that doesn't exist, or to a colleague's Deal/Lead/Prospect the caller
// couldn't edit directly, used to succeed.
func TestAttachmentCreate_ParentMustExistAndBeWritable(t *testing.T) {
	app, db := testutil.App(t)
	repA := testutil.CreateUser(t, db, models.RoleSalesRep)
	repB := testutil.CreateUser(t, db, models.RoleSalesRep)
	manager := testutil.CreateUser(t, db, models.RoleSalesManager)

	deal := seedDeal(t, db, &repA.ID)
	lead := seedLead(t, db, nil)
	require.NoError(t, db.Model(lead).Update("assigned_to", repA.ID).Error)
	prospect := seedProspect(t, db, nil)
	require.NoError(t, db.Model(prospect).Update("assigned_to", repA.ID).Error)
	quote := &models.Quote{DealID: deal.ID}
	require.NoError(t, db.Create(quote).Error)
	link := "https://docs.google.com/document/d/abc"

	cases := []struct {
		name string
		typ  models.AttachmentRelatedType
		id   uint
		user *models.User
		want int
	}{
		{"missing deal", models.AttachmentRelatedDeal, 999999, repA, http.StatusNotFound},
		{"missing company", models.AttachmentRelatedCompany, 999999, repA, http.StatusNotFound},
		{"unknown type", "widget", deal.ID, repA, http.StatusUnprocessableEntity},
		{"other rep's deal", models.AttachmentRelatedDeal, deal.ID, repB, http.StatusForbidden},
		{"other rep's quote", models.AttachmentRelatedQuote, quote.ID, repB, http.StatusForbidden},
		{"other rep's lead", models.AttachmentRelatedLead, lead.ID, repB, http.StatusForbidden},
		{"other rep's prospect", models.AttachmentRelatedProspect, prospect.ID, repB, http.StatusForbidden},
		{"own deal", models.AttachmentRelatedDeal, deal.ID, repA, http.StatusCreated},
		{"manager on any deal", models.AttachmentRelatedDeal, deal.ID, manager, http.StatusCreated},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := testutil.AuthRequest(t, http.MethodPost, "/api/v1/attachments", attachmentLinkBody(tc.typ, tc.id, link), tc.user.ID, tc.user.Role)
			assert.Equal(t, tc.want, doJSON(t, app, req, nil).StatusCode)
		})
	}

	t.Run("multipart upload checks the parent before storing", func(t *testing.T) {
		req := multipartAttachmentRequest(t, 999999, "proposal.pdf", []byte("%PDF-1.4 fake"), testutil.Token(t, repA.ID, repA.Role))
		assert.Equal(t, http.StatusNotFound, doJSON(t, app, req, nil).StatusCode)
	})
}

// TestAttachmentCreate_ExternalURLMustBeHTTP — external_url is rendered as
// a link, so a javascript:/data: URL was a stored-XSS vector.
func TestAttachmentCreate_ExternalURLMustBeHTTP(t *testing.T) {
	app, db := testutil.App(t)
	rep := testutil.CreateUser(t, db, models.RoleSalesRep)
	company := seedCompany(t, db)

	for _, bad := range []string{"javascript:alert(1)", "data:text/html,<script>alert(1)</script>", "ftp://example.com/f", "docs.google.com/abc", "https://"} {
		req := testutil.AuthRequest(t, http.MethodPost, "/api/v1/attachments", attachmentLinkBody(models.AttachmentRelatedCompany, company.ID, bad), rep.ID, rep.Role)
		assert.Equal(t, http.StatusUnprocessableEntity, doJSON(t, app, req, nil).StatusCode, bad)
	}
	for _, good := range []string{"http://intranet.example/doc", "HTTPS://docs.google.com/document/d/abc"} {
		req := testutil.AuthRequest(t, http.MethodPost, "/api/v1/attachments", attachmentLinkBody(models.AttachmentRelatedCompany, company.ID, good), rep.ID, rep.Role)
		assert.Equal(t, http.StatusCreated, doJSON(t, app, req, nil).StatusCode, good)
	}
}

// TestUploads_RequirePasswordChanged — an account still on an
// Admin-assigned password is blocked from every /api/v1 route but the
// password-change ones; /uploads must not be a way around that.
func TestUploads_RequirePasswordChanged(t *testing.T) {
	app, db := testutil.App(t)
	rep := testutil.CreateUser(t, db, models.RoleSalesRep)
	company := seedCompany(t, db)

	req := multipartAttachmentRequest(t, company.ID, "proposal.pdf", []byte("%PDF-1.4 fake"), testutil.Token(t, rep.ID, rep.Role))
	var created struct {
		Data models.Attachment `json:"data"`
	}
	require.Equal(t, http.StatusCreated, doJSON(t, app, req, &created).StatusCode)
	require.NotNil(t, created.Data.FileURL)

	forced := testutil.CreateUser(t, db, models.RoleSalesRep)
	require.NoError(t, db.Model(forced).Update("must_change_password", true).Error)
	get := testutil.AuthRequest(t, http.MethodGet, *created.Data.FileURL, nil, forced.ID, forced.Role)
	assert.Equal(t, http.StatusForbidden, doJSON(t, app, get, nil).StatusCode)
}
