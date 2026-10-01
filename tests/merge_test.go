package apitests

import (
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/testutil"
)

type mergeConflictOut struct {
	Field    string `json:"field"`
	SourceID uint   `json:"source_id"`
	Value    string `json:"value"`
}

type mergeOut struct {
	Data struct {
		Target    map[string]interface{} `json:"target"`
		Moved     map[string]int64       `json:"moved"`
		Filled    []string               `json:"filled"`
		Conflicts []mergeConflictOut     `json:"conflicts"`
	} `json:"data"`
	Error struct {
		Code    string              `json:"code"`
		Message string              `json:"message"`
		Fields  map[string][]string `json:"fields"`
	} `json:"error"`
}

func postMerge(t *testing.T, app *fiber.App, kind string, targetID uint, body interface{}, user *models.User) (*http.Response, mergeOut) {
	t.Helper()
	var out mergeOut
	req := testutil.AuthRequest(t, http.MethodPost, fmt.Sprintf("/api/v1/%s/%d/merge", kind, targetID), body, user.ID, user.Role)
	resp := doJSON(t, app, req, &out)
	return resp, out
}

func strp(s string) *string { return &s }

func seedNamedCompany(t *testing.T, db *gorm.DB, c models.Company) *models.Company {
	t.Helper()
	if c.Status == "" {
		c.Status = models.StatusActive
	}
	require.NoError(t, db.Create(&c).Error)
	return &c
}

// Only Admin/Sales Manager may merge, on both endpoints.
func TestMerge_Roles(t *testing.T) {
	app, db := testutil.App(t)
	target, source := seedCompany(t, db), seedCompany(t, db)
	ct, cs := seedContact(t, db, target.ID), seedContact(t, db, target.ID)

	for _, role := range []models.Role{models.RoleSalesRep, models.RoleMarketing, models.RoleProduction} {
		u := testutil.CreateUser(t, db, role)
		resp, _ := postMerge(t, app, "companies", target.ID, map[string]interface{}{"source_ids": []uint{source.ID}}, u)
		assert.Equal(t, http.StatusForbidden, resp.StatusCode, role)
		resp, _ = postMerge(t, app, "contacts", ct.ID, map[string]interface{}{"source_ids": []uint{cs.ID}}, u)
		assert.Equal(t, http.StatusForbidden, resp.StatusCode, role)
	}

	manager := testutil.CreateUser(t, db, models.RoleSalesManager)
	resp, _ := postMerge(t, app, "companies", target.ID, map[string]interface{}{"source_ids": []uint{source.ID}}, manager)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	resp, _ = postMerge(t, app, "contacts", ct.ID, map[string]interface{}{"source_ids": []uint{cs.ID}}, admin)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

// 422 on source_ids for empty/missing, over 20, the target itself and
// duplicates; 404 naming every missing or soft-deleted id.
func TestMerge_ValidationAndNotFound(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	target, source := seedCompany(t, db), seedCompany(t, db)
	deleted := seedCompany(t, db)
	require.NoError(t, db.Delete(deleted).Error)

	tooMany := make([]uint, 21)
	for i := range tooMany {
		tooMany[i] = uint(100000 + i)
	}
	for name, body := range map[string]interface{}{
		"missing":   map[string]interface{}{},
		"empty":     map[string]interface{}{"source_ids": []uint{}},
		"too many":  map[string]interface{}{"source_ids": tooMany},
		"target":    map[string]interface{}{"source_ids": []uint{source.ID, target.ID}},
		"duplicate": map[string]interface{}{"source_ids": []uint{source.ID, source.ID}},
	} {
		for _, kind := range []string{"companies", "contacts"} {
			resp, out := postMerge(t, app, kind, target.ID, body, admin)
			assert.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode, name+" "+kind)
			assert.Contains(t, out.Error.Fields, "source_ids", name+" "+kind)
		}
	}

	resp, out := postMerge(t, app, "companies", target.ID, map[string]interface{}{"source_ids": []uint{source.ID, deleted.ID, 999999}}, admin)
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
	assert.Contains(t, out.Error.Message, itoa(deleted.ID))
	assert.Contains(t, out.Error.Message, "999999")
	assert.NotContains(t, out.Error.Message, itoa(source.ID)+",")

	resp, out = postMerge(t, app, "companies", deleted.ID, map[string]interface{}{"source_ids": []uint{source.ID}}, admin)
	require.Equal(t, http.StatusNotFound, resp.StatusCode, "deleted target")
	assert.Contains(t, out.Error.Message, itoa(deleted.ID))

	resp, out = postMerge(t, app, "contacts", 999998, map[string]interface{}{"source_ids": []uint{999997}}, admin)
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
	assert.Contains(t, out.Error.Message, "999997")
	assert.Contains(t, out.Error.Message, "999998")

	var req = testutil.AuthRequest(t, http.MethodPost, "/api/v1/companies/abc/merge", map[string]interface{}{"source_ids": []uint{source.ID}}, admin.ID, admin.Role)
	assert.Equal(t, http.StatusNotFound, doJSON(t, app, req, nil).StatusCode)

	// Nothing was touched by the failed requests.
	var still models.Company
	require.NoError(t, db.First(&still, source.ID).Error)
}

// A Company merge re-points one row of every referencing table (including a
// soft-deleted Deal), fills empty fields, unions tags, reports identity
// conflicts, soft-deletes the sources and writes the audit rows.
func TestMerge_Companies(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)

	target := seedNamedCompany(t, db, models.Company{
		Name: "Acme", Website: "https://acme.example", Domain: "acme.example",
		Tags: pq.StringArray{"vip"}, TaxID: strp("0105551234567"),
	})
	src1 := seedNamedCompany(t, db, models.Company{
		Name: "ACME Ltd", Industry: "Software", Website: "https://acme-ltd.example", Domain: "acme-ltd.example",
		Tags: pq.StringArray{"VIP", "Enterprise"}, TaxID: strp("0105551234567"), BranchCode: strp("00000"),
		LegalName: strp("Acme Co., Ltd."),
	})
	src2 := seedNamedCompany(t, db, models.Company{
		Name: "Acme Thailand", Industry: "Retail", Size: "51-200", Notes: "from src2",
		Tags: pq.StringArray{"partner"}, TaxID: strp("0999999999999"), Address: strp("Bangkok"),
	})
	sources := []uint{src1.ID, src2.ID}

	// One row of every referencing table, spread over the sources.
	contact := seedContact(t, db, src1.ID)
	primary := &models.Contact{CompanyID: src2.ID, Name: "Primary", Status: models.StatusActive, IsPrimary: true}
	require.NoError(t, db.Create(primary).Error)
	targetPrimary := &models.Contact{CompanyID: target.ID, Name: "Target primary", Status: models.StatusActive, IsPrimary: true}
	require.NoError(t, db.Create(targetPrimary).Error)
	deal := &models.Deal{CompanyID: src1.ID, ContactID: contact.ID, Title: "D", Stage: models.DealStageLead, Status: models.DealStatusOpen}
	require.NoError(t, db.Create(deal).Error)
	trashedDeal := &models.Deal{CompanyID: src2.ID, ContactID: contact.ID, Title: "Trashed", Stage: models.DealStageLead, Status: models.DealStatusLost}
	require.NoError(t, db.Create(trashedDeal).Error)
	require.NoError(t, db.Delete(trashedDeal).Error)
	lead := &models.Lead{Name: "L", CompanyID: &src1.ID, Source: models.LeadSourceWebsite, Status: models.LeadStatusNew}
	require.NoError(t, db.Create(lead).Error)
	refType := models.RelatedTypeCompany
	referral := &models.Lead{Name: "Referred", Source: models.LeadSourceReferral, Status: models.LeadStatusNew, ReferredByType: &refType, ReferredByID: &src2.ID}
	require.NoError(t, db.Create(referral).Error)
	prospect := seedProspect(t, db, &src2.ID)
	project := &models.Project{CompanyID: src1.ID, Name: "P", StartDate: time.Now()}
	require.NoError(t, db.Create(project).Error)
	product := &models.Product{Name: "CRM", IsActive: true}
	require.NoError(t, db.Create(product).Error)
	cp := &models.CustomerProduct{CompanyID: src2.ID, ProductID: product.ID, StartDate: time.Now()}
	require.NoError(t, db.Create(cp).Error)
	activity := &models.Activity{Type: models.ActivityTypeCall, Subject: "call", RelatedType: models.RelatedTypeCompany, RelatedID: src1.ID, CreatedByID: admin.ID}
	require.NoError(t, db.Create(activity).Error)
	// Same related_id but another type: must not move.
	dealActivity := &models.Activity{Type: models.ActivityTypeNote, Subject: "deal note", RelatedType: models.RelatedTypeDeal, RelatedID: src1.ID, CreatedByID: admin.ID}
	require.NoError(t, db.Create(dealActivity).Error)
	attachment := &models.Attachment{RelatedType: models.AttachmentRelatedCompany, RelatedID: src2.ID, Category: models.AttachmentCategoryOther, FileName: "a.pdf", UploadedByID: admin.ID}
	require.NoError(t, db.Create(attachment).Error)
	task := &models.Task{RelatedType: models.RelatedTypeCompany, RelatedID: src1.ID, Title: "T", DueDate: time.Now()}
	require.NoError(t, db.Create(task).Error)
	rule := &models.NotificationRule{Name: "merge dormant", EntityType: models.NotificationEntityCompany, ThresholdDays: 60, RecipientRole: models.NotificationRecipientOwner, IsActive: true}
	require.NoError(t, db.Create(rule).Error)
	movedLog := &models.NotificationLog{RuleID: rule.ID, EntityID: src1.ID, Context: "60", NotifiedAt: time.Now()}
	require.NoError(t, db.Create(movedLog).Error)
	targetLog := &models.NotificationLog{RuleID: rule.ID, EntityID: target.ID, Context: "90", NotifiedAt: time.Now()}
	require.NoError(t, db.Create(targetLog).Error)
	keptLog := &models.NotificationLog{RuleID: rule.ID, EntityID: src2.ID, Context: "90", NotifiedAt: time.Now()}
	require.NoError(t, db.Create(keptLog).Error)

	resp, out := postMerge(t, app, "companies", target.ID, map[string]interface{}{"source_ids": sources}, admin)
	require.Equal(t, http.StatusOK, resp.StatusCode, out.Error.Message)

	assert.Equal(t, map[string]int64{
		"contacts": 2, "deals": 2, "leads": 1, "prospects": 1, "projects": 1, "customer_products": 1,
		"activities": 1, "attachments": 1, "tasks": 1, "lead_referrals": 1, "notification_logs": 1, "total": 13,
	}, out.Data.Moved)

	countAt := func(model interface{}, where string, args ...interface{}) int64 {
		var n int64
		require.NoError(t, db.Unscoped().Model(model).Where(where, args...).Count(&n).Error)
		return n
	}
	assert.EqualValues(t, 3, countAt(&models.Contact{}, "company_id = ?", target.ID))
	assert.EqualValues(t, 2, countAt(&models.Deal{}, "company_id = ?", target.ID), "incl. the trashed Deal")
	assert.EqualValues(t, 1, countAt(&models.Lead{}, "id = ? AND company_id = ?", lead.ID, target.ID))
	assert.EqualValues(t, 1, countAt(&models.Lead{}, "id = ? AND referred_by_id = ?", referral.ID, target.ID))
	assert.EqualValues(t, 1, countAt(&models.Prospect{}, "id = ? AND company_id = ?", prospect.ID, target.ID))
	assert.EqualValues(t, 1, countAt(&models.Project{}, "id = ? AND company_id = ?", project.ID, target.ID))
	assert.EqualValues(t, 1, countAt(&models.CustomerProduct{}, "id = ? AND company_id = ?", cp.ID, target.ID))
	assert.EqualValues(t, 1, countAt(&models.Activity{}, "id = ? AND related_id = ?", activity.ID, target.ID))
	assert.EqualValues(t, 1, countAt(&models.Activity{}, "id = ? AND related_id = ?", dealActivity.ID, src1.ID), "a deal Activity isn't a company reference")
	assert.EqualValues(t, 1, countAt(&models.Attachment{}, "id = ? AND related_id = ?", attachment.ID, target.ID))
	assert.EqualValues(t, 1, countAt(&models.Task{}, "id = ? AND related_id = ?", task.ID, target.ID))
	assert.EqualValues(t, 1, countAt(&models.NotificationLog{}, "id = ? AND entity_id = ?", movedLog.ID, target.ID))
	assert.EqualValues(t, 1, countAt(&models.NotificationLog{}, "id = ? AND entity_id = ?", keptLog.ID, src2.ID), "target already has that tier")

	// The target's own Primary Contact wins; the moved one is cleared.
	assert.EqualValues(t, 1, countAt(&models.Contact{}, "company_id = ? AND is_primary", target.ID))
	assert.EqualValues(t, 1, countAt(&models.Contact{}, "id = ? AND is_primary", targetPrimary.ID))

	// Fields.
	var got models.Company
	require.NoError(t, db.First(&got, target.ID).Error)
	assert.Equal(t, "Acme", got.Name, "non-empty target field kept")
	assert.Equal(t, "Software", got.Industry, "first source in order wins")
	assert.Equal(t, "51-200", got.Size)
	assert.Equal(t, "from src2", got.Notes)
	assert.Equal(t, "https://acme.example", got.Website)
	assert.Equal(t, "acme.example", got.Domain)
	assert.Equal(t, "0105551234567", *got.TaxID)
	require.NotNil(t, got.BranchCode, "same tax_id: branch filled")
	assert.Equal(t, "00000", *got.BranchCode)
	assert.Equal(t, "Acme Co., Ltd.", *got.LegalName)
	assert.Equal(t, "Bangkok", *got.Address)
	assert.Equal(t, pq.StringArray{"vip", "enterprise", "partner"}, got.Tags)
	assert.ElementsMatch(t, []string{"industry", "size", "notes", "legal_name", "address", "branch_code", "tags"}, out.Data.Filled)
	assert.ElementsMatch(t, []mergeConflictOut{
		{Field: "website", SourceID: src1.ID, Value: "https://acme-ltd.example"},
		{Field: "tax_id", SourceID: src2.ID, Value: "0999999999999"},
	}, out.Data.Conflicts)
	assert.Equal(t, "Acme", out.Data.Target["name"])
	assert.Contains(t, out.Data.Target, "last_activity_at")
	assert.NotNil(t, out.Data.Target["last_activity_at"], "moved company Activity counts")

	// Sources soft-deleted (in Trash), domain cleared so a restore can't collide.
	for _, sid := range sources {
		var s models.Company
		require.NoError(t, db.Unscoped().First(&s, sid).Error)
		assert.True(t, s.DeletedAt.Valid)
		require.NotNil(t, s.DeletedBy)
		assert.Equal(t, admin.ID, *s.DeletedBy)
		assert.Empty(t, s.Domain)
	}
	resp = doJSON(t, app, testutil.AuthRequest(t, http.MethodPost, "/api/v1/companies/"+itoa(src1.ID)+"/restore", nil, admin.ID, admin.Role), nil)
	assert.Equal(t, http.StatusOK, resp.StatusCode, "a merged source is restorable")

	// Audit.
	var merged models.AuditLogEntry
	require.NoError(t, db.Where("entity_type = ? AND entity_id = ? AND action = ?", "company", target.ID, "merged").First(&merged).Error)
	assert.Equal(t, admin.ID, merged.ActorID)
	assert.Equal(t, "Acme", merged.Before["name"])
	assert.Equal(t, []interface{}{float64(src1.ID), float64(src2.ID)}, merged.After["source_ids"])
	assert.Contains(t, merged.After, "moved")
	assert.Contains(t, merged.After, "filled")
	for _, sid := range sources {
		var into models.AuditLogEntry
		require.NoError(t, db.Where("entity_type = ? AND entity_id = ? AND action = ?", "company", sid, "merged_into").First(&into).Error)
		assert.Equal(t, float64(target.ID), into.After["target_id"])
	}
}

// Without a target Primary, the first source (in source_ids order) with one
// keeps it and no other moved Contact stays Primary.
func TestMerge_CompaniesPrimaryContactFromFirstSource(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	target, a, b := seedCompany(t, db), seedCompany(t, db), seedCompany(t, db)
	pa := &models.Contact{CompanyID: a.ID, Name: "A", Status: models.StatusActive, IsPrimary: true}
	pb := &models.Contact{CompanyID: b.ID, Name: "B", Status: models.StatusActive, IsPrimary: true}
	require.NoError(t, db.Create(pa).Error)
	require.NoError(t, db.Create(pb).Error)

	resp, _ := postMerge(t, app, "companies", target.ID, map[string]interface{}{"source_ids": []uint{b.ID, a.ID}}, admin)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var primaries []uint
	require.NoError(t, db.Model(&models.Contact{}).Where("company_id = ? AND is_primary", target.ID).Pluck("id", &primaries).Error)
	assert.Equal(t, []uint{pb.ID}, primaries)
}

// A Contact merge takes sources from other Companies, keeps the target's
// company_id, re-points deals/activities/tasks/referrals, and reports a
// different email/phone instead of copying it.
func TestMerge_ContactsAcrossCompanies(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	home, other := seedCompany(t, db), seedCompany(t, db)

	target := &models.Contact{CompanyID: home.ID, Name: "Jane", Email: "jane@example.com", Status: models.StatusActive, Tags: pq.StringArray{"a"}}
	require.NoError(t, db.Create(target).Error)
	src1 := &models.Contact{CompanyID: other.ID, Name: "Jane D", Email: "JANE@example.com", Phone: "081-234-5678", RoleTitle: "CTO", Status: models.StatusActive, Tags: pq.StringArray{"B"}, IsPrimary: true}
	src2 := &models.Contact{CompanyID: other.ID, Name: "J. Doe", Email: "jdoe@other.example", Phone: "+66 81 234 5678", Status: models.StatusActive}
	src3 := &models.Contact{CompanyID: home.ID, Name: "Jane 3", Phone: "02-000-0000", Status: models.StatusActive}
	for _, c := range []*models.Contact{src1, src2, src3} {
		require.NoError(t, db.Create(c).Error)
	}
	sources := []uint{src1.ID, src2.ID, src3.ID}

	deal := &models.Deal{CompanyID: other.ID, ContactID: src1.ID, Title: "D", Stage: models.DealStageLead, Status: models.DealStatusOpen}
	require.NoError(t, db.Create(deal).Error)
	activity := &models.Activity{Type: models.ActivityTypeCall, Subject: "c", RelatedType: models.RelatedTypeContact, RelatedID: src2.ID, CreatedByID: admin.ID}
	require.NoError(t, db.Create(activity).Error)
	companyActivity := &models.Activity{Type: models.ActivityTypeCall, Subject: "c", RelatedType: models.RelatedTypeCompany, RelatedID: src2.ID, CreatedByID: admin.ID}
	require.NoError(t, db.Create(companyActivity).Error)
	task := &models.Task{RelatedType: models.RelatedTypeContact, RelatedID: src3.ID, Title: "T", DueDate: time.Now()}
	require.NoError(t, db.Create(task).Error)
	refType := models.RelatedTypeContact
	referral := &models.Lead{Name: "R", Source: models.LeadSourceReferral, Status: models.LeadStatusNew, ReferredByType: &refType, ReferredByID: &src1.ID}
	require.NoError(t, db.Create(referral).Error)

	resp, out := postMerge(t, app, "contacts", target.ID, map[string]interface{}{"source_ids": sources}, admin)
	require.Equal(t, http.StatusOK, resp.StatusCode, out.Error.Message)
	assert.Equal(t, map[string]int64{"deals": 1, "activities": 1, "tasks": 1, "lead_referrals": 1, "total": 4}, out.Data.Moved)

	var d models.Deal
	require.NoError(t, db.First(&d, deal.ID).Error)
	assert.Equal(t, target.ID, d.ContactID)
	assert.Equal(t, other.ID, d.CompanyID, "the Deal keeps its own Company")
	var a models.Activity
	require.NoError(t, db.First(&a, activity.ID).Error)
	assert.Equal(t, target.ID, a.RelatedID)
	var ca models.Activity
	require.NoError(t, db.First(&ca, companyActivity.ID).Error)
	assert.Equal(t, src2.ID, ca.RelatedID, "a company Activity isn't a contact reference")
	var tk models.Task
	require.NoError(t, db.First(&tk, task.ID).Error)
	assert.Equal(t, target.ID, tk.RelatedID)
	var l models.Lead
	require.NoError(t, db.First(&l, referral.ID).Error)
	assert.Equal(t, target.ID, *l.ReferredByID)

	var got models.Contact
	require.NoError(t, db.First(&got, target.ID).Error)
	assert.Equal(t, home.ID, got.CompanyID)
	assert.Equal(t, "Jane", got.Name)
	assert.Equal(t, "jane@example.com", got.Email)
	assert.Equal(t, "081-234-5678", got.Phone)
	assert.Equal(t, "CTO", got.RoleTitle)
	assert.False(t, got.IsPrimary, "a Primary in another Company isn't carried over")
	assert.Equal(t, pq.StringArray{"a", "b"}, got.Tags)
	assert.ElementsMatch(t, []string{"phone", "role_title", "tags"}, out.Data.Filled)
	// src1's email differs only in case; src2's phone is the same number.
	assert.ElementsMatch(t, []mergeConflictOut{
		{Field: "email", SourceID: src2.ID, Value: "jdoe@other.example"},
		{Field: "phone", SourceID: src3.ID, Value: "02-000-0000"},
	}, out.Data.Conflicts)
	assert.EqualValues(t, home.ID, out.Data.Target["company_id"])

	var live int64
	require.NoError(t, db.Model(&models.Contact{}).Where("id IN ?", sources).Count(&live).Error)
	assert.Zero(t, live, "sources soft-deleted")
	var audits int64
	require.NoError(t, db.Model(&models.AuditLogEntry{}).Where("entity_type = 'contact' AND action = 'merged_into' AND entity_id IN ?", sources).Count(&audits).Error)
	assert.EqualValues(t, 3, audits)
	require.NoError(t, db.Model(&models.AuditLogEntry{}).Where("entity_type = 'contact' AND action = 'merged' AND entity_id = ?", target.ID).Count(&audits).Error)
	assert.EqualValues(t, 1, audits)

	// A merged-away source no longer counts as a duplicate.
	resp = doJSON(t, app, testutil.AuthRequest(t, http.MethodPost, "/api/v1/contacts",
		map[string]interface{}{"company_id": home.ID, "name": "New", "email": "jdoe@other.example"}, admin.ID, admin.Role), nil)
	assert.Equal(t, http.StatusCreated, resp.StatusCode)
}

// A same-Company Primary source makes the target Primary.
func TestMerge_ContactsPrimaryFromSameCompany(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	company := seedCompany(t, db)
	target := seedContact(t, db, company.ID)
	src := &models.Contact{CompanyID: company.ID, Name: "P", Status: models.StatusActive, IsPrimary: true}
	require.NoError(t, db.Create(src).Error)

	resp, out := postMerge(t, app, "contacts", target.ID, map[string]interface{}{"source_ids": []uint{src.ID}}, admin)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, out.Data.Filled, "is_primary")
	assert.Equal(t, true, out.Data.Target["is_primary"])
}

// Merges running at once over overlapping records queue on the row locks
// instead of deadlocking: of two opposite merges exactly one wins and the
// other gets 404 (its source is gone), and an overlapping Contact merge
// still succeeds. A deadlock would surface as a 500.
func TestMerge_ConcurrentMergesDontDeadlock(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)

	for round := 0; round < 5; round++ {
		a, b, c := seedCompany(t, db), seedCompany(t, db), seedCompany(t, db)
		ca, cb := seedContact(t, db, a.ID), seedContact(t, db, b.ID)
		for _, id := range []uint{a.ID, b.ID} {
			require.NoError(t, db.Create(&models.Deal{CompanyID: id, ContactID: ca.ID, Title: "x", Stage: models.DealStageLead, Status: models.DealStatusOpen}).Error)
			require.NoError(t, db.Create(&models.Deal{CompanyID: id, ContactID: cb.ID, Title: "y", Stage: models.DealStageLead, Status: models.DealStatusOpen}).Error)
		}

		type call struct {
			kind   string
			target uint
			src    []uint
		}
		calls := []call{
			{"companies", a.ID, []uint{c.ID, b.ID}},
			{"companies", b.ID, []uint{a.ID, c.ID}},
			{"contacts", cb.ID, []uint{ca.ID}},
		}
		statuses := make([]int, len(calls))
		var wg sync.WaitGroup
		start := make(chan struct{})
		for i, cl := range calls {
			wg.Add(1)
			go func(i int, cl call) {
				defer wg.Done()
				<-start
				req := testutil.AuthRequest(t, http.MethodPost, fmt.Sprintf("/api/v1/%s/%d/merge", cl.kind, cl.target),
					map[string]interface{}{"source_ids": cl.src}, admin.ID, admin.Role)
				resp, err := app.Test(req, -1)
				if err == nil {
					statuses[i] = resp.StatusCode
				}
			}(i, cl)
		}
		close(start)
		wg.Wait()

		assert.ElementsMatch(t, []int{http.StatusOK, http.StatusNotFound}, statuses[:2], "round %d", round)
		assert.Equal(t, http.StatusOK, statuses[2], "round %d", round)

		var live int64
		require.NoError(t, db.Model(&models.Company{}).Where("id IN ?", []uint{a.ID, b.ID, c.ID}).Count(&live).Error)
		assert.EqualValues(t, 1, live, "round %d", round)
		var stray int64
		require.NoError(t, db.Model(&models.Deal{}).Where("contact_id = ?", ca.ID).Count(&stray).Error)
		assert.Zero(t, stray, "round %d", round)
	}
}
