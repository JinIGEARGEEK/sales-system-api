package apitests

import (
	"net/http"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/stretchr/testify/require"

	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/testutil"
)

// TestDashboardLeadSummary_CountsByStatus guards LeadSummary's status
// breakdown/total — new_leads, qualified_leads, disqualified_leads must each
// reflect the actual seeded rows, mirroring the shape
// TestDashboardProspectSummary_CountsByStatus below checks for Prospect.
func TestDashboardLeadSummary_CountsByStatus(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)

	require.NoError(t, db.Create(&models.Lead{Name: "New Lead", Source: models.LeadSourceWebsite, Status: models.LeadStatusNew}).Error)
	require.NoError(t, db.Create(&models.Lead{Name: "Qualified Lead", Source: models.LeadSourceWebsite, Status: models.LeadStatusQualified}).Error)
	require.NoError(t, db.Create(&models.Lead{Name: "Disqualified Lead", Source: models.LeadSourceWebsite, Status: models.LeadStatusDisqualified}).Error)

	req := testutil.AuthRequest(t, http.MethodGet, "/api/v1/dashboard/lead-summary", nil, admin.ID, admin.Role)
	var out struct {
		Data struct {
			TotalLeads        int64 `json:"total_leads"`
			NewLeads          int64 `json:"new_leads"`
			QualifiedLeads    int64 `json:"qualified_leads"`
			DisqualifiedLeads int64 `json:"disqualified_leads"`
		} `json:"data"`
	}
	resp := doJSON(t, app, req, &out)
	require.Equal(t, fiber.StatusOK, resp.StatusCode)
	require.Equal(t, int64(3), out.Data.TotalLeads)
	require.Equal(t, int64(1), out.Data.NewLeads)
	require.Equal(t, int64(1), out.Data.QualifiedLeads)
	require.Equal(t, int64(1), out.Data.DisqualifiedLeads)
}

// TestDashboardLeadSummary_FiltersByAssignedTo guards the assigned_to query
// param scoping every aggregate down to one rep's Leads.
func TestDashboardLeadSummary_FiltersByAssignedTo(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	rep := testutil.CreateUser(t, db, models.RoleSalesRep)

	require.NoError(t, db.Create(&models.Lead{Name: "Rep's Lead", Source: models.LeadSourceWebsite, Status: models.LeadStatusNew, AssignedTo: &rep.ID}).Error)
	require.NoError(t, db.Create(&models.Lead{Name: "Unrelated Lead", Source: models.LeadSourceWebsite, Status: models.LeadStatusNew}).Error)

	req := testutil.AuthRequest(t, http.MethodGet, "/api/v1/dashboard/lead-summary?assigned_to="+itoa(rep.ID), nil, admin.ID, admin.Role)
	var out struct {
		Data struct {
			TotalLeads int64 `json:"total_leads"`
		} `json:"data"`
	}
	resp := doJSON(t, app, req, &out)
	require.Equal(t, fiber.StatusOK, resp.StatusCode)
	require.Equal(t, int64(1), out.Data.TotalLeads)
}

// TestDashboardProspectSummary_CountsByStatus guards ProspectSummary's
// total/open/converted counts and conversion_rate computation.
func TestDashboardProspectSummary_CountsByStatus(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)

	require.NoError(t, db.Create(&models.Prospect{Name: "New Prospect", Source: "Social Media", Status: models.ProspectStatusNew}).Error)
	require.NoError(t, db.Create(&models.Prospect{Name: "Converted Prospect", Source: "Social Media", Status: models.ProspectStatusConverted}).Error)
	require.NoError(t, db.Create(&models.Prospect{Name: "Disqualified Prospect", Source: "Social Media", Status: models.ProspectStatusDisqualified}).Error)

	req := testutil.AuthRequest(t, http.MethodGet, "/api/v1/dashboard/prospect-summary", nil, admin.ID, admin.Role)
	var out struct {
		Data struct {
			TotalProspects int64   `json:"total_prospects"`
			OpenProspects  int64   `json:"open_prospects"`
			ConvertedCount int64   `json:"converted_count"`
			ConversionRate float64 `json:"conversion_rate"`
		} `json:"data"`
	}
	resp := doJSON(t, app, req, &out)
	require.Equal(t, fiber.StatusOK, resp.StatusCode)
	require.Equal(t, int64(3), out.Data.TotalProspects)
	require.Equal(t, int64(1), out.Data.OpenProspects) // "New" only — Converted/Disqualified are excluded
	require.Equal(t, int64(1), out.Data.ConvertedCount)
	require.InDelta(t, float64(1)/float64(3)*100, out.Data.ConversionRate, 0.01)
}

// TestDashboardSummaries_OpenToAnyAuthenticatedRole guards that neither
// dashboard tab is RequireRoles-gated at the route — the frontend decides
// which role sees which tab, per both handlers' own doc comments.
func TestDashboardSummaries_OpenToAnyAuthenticatedRole(t *testing.T) {
	app, db := testutil.App(t)
	marketing := testutil.CreateUser(t, db, models.RoleMarketing)

	leadReq := testutil.AuthRequest(t, http.MethodGet, "/api/v1/dashboard/lead-summary", nil, marketing.ID, marketing.Role)
	require.Equal(t, fiber.StatusOK, doJSON(t, app, leadReq, nil).StatusCode)

	prospectReq := testutil.AuthRequest(t, http.MethodGet, "/api/v1/dashboard/prospect-summary", nil, marketing.ID, marketing.Role)
	require.Equal(t, fiber.StatusOK, doJSON(t, app, prospectReq, nil).StatusCode)
}
