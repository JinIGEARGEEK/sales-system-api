package apitests

import (
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/testutil"
)

// Server-side paging/filtering for the Tasks and Activities list pages
// (previously one capped fetch filtered client-side): GET /tasks' search/
// business_unit/due-date/unassigned/related_type-only filters, GET
// /activities' search/related_type-only filters and include_stage_changes
// feed, and GET /audit-log honoring `action`.

type pagedTasks struct {
	Data  []models.Task `json:"data"`
	Total int64         `json:"total"`
}

func listTasks(t *testing.T, env taskEnv, query string) pagedTasks {
	t.Helper()
	var out pagedTasks
	req := testutil.AuthRequest(t, http.MethodGet, "/api/v1/tasks?"+query, nil, env.admin.ID, env.admin.Role)
	resp := doJSON(t, env.app, req, &out)
	require.Equal(t, http.StatusOK, resp.StatusCode, query)
	return out
}

type taskEnv struct {
	app   *fiber.App
	admin *models.User
}

func createTask(t *testing.T, db *gorm.DB, task models.Task) *models.Task {
	t.Helper()
	if task.Status == "" {
		task.Status = models.TaskStatusPending
	}
	require.NoError(t, db.Create(&task).Error)
	return &task
}

func taskIDs(tasks []models.Task) []uint {
	ids := make([]uint, 0, len(tasks))
	for _, task := range tasks {
		ids = append(ids, task.ID)
	}
	return ids
}

func TestTasksList_ServerFilters(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	env := taskEnv{app: app, admin: admin}

	projectDeal := seedDeal(t, db, nil)
	require.NoError(t, db.Model(&models.Deal{}).Where("id = ?", projectDeal.ID).
		Updates(map[string]interface{}{"title": "Website Redesign", "business_unit": models.BusinessUnitProject}).Error)
	productDeal := seedDeal(t, db, nil)
	require.NoError(t, db.Model(&models.Deal{}).Where("id = ?", productDeal.ID).
		Updates(map[string]interface{}{"title": "License Renewal", "business_unit": models.BusinessUnitProduct}).Error)
	company := seedCompany(t, db)

	now := time.Now()
	callClient := createTask(t, db, models.Task{RelatedType: "deal", RelatedID: projectDeal.ID, Title: "Call the client", DueDate: now.AddDate(0, 0, -2), AssignedTo: &admin.ID})
	sendQuote := createTask(t, db, models.Task{RelatedType: "deal", RelatedID: productDeal.ID, Title: "Send quote", Description: "pricing sheet", DueDate: now.AddDate(0, 0, 3)})
	companyTask := createTask(t, db, models.Task{RelatedType: "company", RelatedID: company.ID, Title: "Check in", DueDate: now.AddDate(0, 0, 10)})

	t.Run("search matches title, description and the linked record's name", func(t *testing.T) {
		assert.ElementsMatch(t, []uint{callClient.ID}, taskIDs(listTasks(t, env, "search=call").Data))
		assert.ElementsMatch(t, []uint{sendQuote.ID}, taskIDs(listTasks(t, env, "search=PRICING").Data))
		assert.ElementsMatch(t, []uint{callClient.ID}, taskIDs(listTasks(t, env, "search=redesign").Data), "deal title")
		assert.ElementsMatch(t, []uint{companyTask.ID}, taskIDs(listTasks(t, env, "search=acme").Data), "company name")
	})

	t.Run("assigned_to=unassigned and related_type alone", func(t *testing.T) {
		assert.ElementsMatch(t, []uint{sendQuote.ID, companyTask.ID}, taskIDs(listTasks(t, env, "assigned_to=unassigned").Data))
		assert.ElementsMatch(t, []uint{companyTask.ID}, taskIDs(listTasks(t, env, "related_type=company").Data))
	})

	t.Run("business_unit follows the linked deal", func(t *testing.T) {
		assert.ElementsMatch(t, []uint{callClient.ID}, taskIDs(listTasks(t, env, "business_unit=Project").Data))
		assert.ElementsMatch(t, []uint{sendQuote.ID}, taskIDs(listTasks(t, env, "business_unit=Product").Data))
	})

	t.Run("due_from/due_before bound the due date (RFC 3339 or date)", func(t *testing.T) {
		today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
		q := url.Values{"due_before": {today.Format(time.RFC3339)}}
		assert.ElementsMatch(t, []uint{callClient.ID}, taskIDs(listTasks(t, env, q.Encode()).Data), "overdue bucket")
		q = url.Values{"due_from": {today.Format(time.RFC3339)}, "due_before": {today.AddDate(0, 0, 5).Format(time.RFC3339)}}
		assert.ElementsMatch(t, []uint{sendQuote.ID}, taskIDs(listTasks(t, env, q.Encode()).Data))
		q = url.Values{"due_from": {today.AddDate(0, 0, 5).Format("2006-01-02")}}
		assert.ElementsMatch(t, []uint{companyTask.ID}, taskIDs(listTasks(t, env, q.Encode()).Data))

		req := testutil.AuthRequest(t, http.MethodGet, "/api/v1/tasks?due_from=yesterday", nil, admin.ID, admin.Role)
		resp := doJSON(t, app, req, nil)
		assert.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)
	})

	t.Run("paging reports the full total and sorts by due date", func(t *testing.T) {
		page := listTasks(t, env, "per_page=2&page=1&sort=due_date")
		assert.EqualValues(t, 3, page.Total)
		assert.Equal(t, []uint{callClient.ID, sendQuote.ID}, taskIDs(page.Data))
		page = listTasks(t, env, "per_page=2&page=2&sort=due_date")
		assert.Equal(t, []uint{companyTask.ID}, taskIDs(page.Data))
	})

	t.Run("related_type+related_id still scopes a detail page's tab", func(t *testing.T) {
		assert.ElementsMatch(t, []uint{sendQuote.ID}, taskIDs(listTasks(t, env, "related_type=deal&related_id="+itoa(productDeal.ID)).Data))
	})
}

// Search terms are matched literally: LIKE's % and _ wildcards (and the \
// escape character) in the user's input must not act as wildcards.
func TestTaskAndActivitySearch_EscapesLikeWildcards(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	env := taskEnv{app: app, admin: admin}
	deal := seedDeal(t, db, nil)
	due := time.Now().AddDate(0, 0, 1)

	percent := createTask(t, db, models.Task{RelatedType: "deal", RelatedID: deal.ID, Title: "Offer 100% discount", DueDate: due})
	createTask(t, db, models.Task{RelatedType: "deal", RelatedID: deal.ID, Title: "Offer 1000 discount", DueDate: due})
	underscore := createTask(t, db, models.Task{RelatedType: "deal", RelatedID: deal.ID, Title: "Rename file_a", DueDate: due})
	createTask(t, db, models.Task{RelatedType: "deal", RelatedID: deal.ID, Title: "Rename filexa", DueDate: due})
	backslash := createTask(t, db, models.Task{RelatedType: "deal", RelatedID: deal.ID, Title: `Copy C:\share`, DueDate: due})

	q := func(search string) string { return url.Values{"search": {search}}.Encode() }
	assert.ElementsMatch(t, []uint{percent.ID}, taskIDs(listTasks(t, env, q("100%")).Data))
	assert.ElementsMatch(t, []uint{underscore.ID}, taskIDs(listTasks(t, env, q("file_a")).Data))
	assert.ElementsMatch(t, []uint{backslash.ID}, taskIDs(listTasks(t, env, q(`C:\share`)).Data))
	assert.ElementsMatch(t, []uint{percent.ID}, taskIDs(listTasks(t, env, q("%")).Data), "a lone % matches only a literal %")

	literal := &models.Activity{Type: models.ActivityTypeNote, Subject: "50% deposit", RelatedType: models.RelatedTypeDeal, RelatedID: deal.ID, CreatedByID: admin.ID}
	require.NoError(t, db.Create(literal).Error)
	require.NoError(t, db.Create(&models.Activity{Type: models.ActivityTypeNote, Subject: "500 deposit", RelatedType: models.RelatedTypeDeal, RelatedID: deal.ID, CreatedByID: admin.ID}).Error)
	for _, extra := range []string{"", "&include_stage_changes=true"} {
		var out pagedFeed
		req := testutil.AuthRequest(t, http.MethodGet, "/api/v1/activities?"+q("50%")+extra, nil, admin.ID, admin.Role)
		require.Equal(t, http.StatusOK, doJSON(t, app, req, &out).StatusCode)
		require.Len(t, out.Data, 1, extra)
		assert.Equal(t, literal.ID, out.Data[0].ID, extra)
	}
}

type feedItem struct {
	ID          uint   `json:"id"`
	Kind        string `json:"kind"`
	Type        string `json:"type"`
	Subject     string `json:"subject"`
	RelatedType string `json:"related_type"`
	RelatedID   uint   `json:"related_id"`
	CreatedBy   string `json:"created_by"`
	FromStage   string `json:"from_stage"`
	ToStage     string `json:"to_stage"`
}

type pagedFeed struct {
	Data  []feedItem `json:"data"`
	Total int64      `json:"total"`
}

func TestActivitiesList_FiltersAndStageChangeFeed(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	deal := seedDeal(t, db, nil)
	require.NoError(t, db.Model(&models.Deal{}).Where("id = ?", deal.ID).Update("title", "Website Redesign").Error)
	company := seedCompany(t, db)

	base := time.Now().Add(-time.Hour)
	call := &models.Activity{Type: models.ActivityTypeCall, Subject: "Intro call", RelatedType: models.RelatedTypeDeal, RelatedID: deal.ID, CreatedByID: admin.ID}
	call.CreatedAt = base
	require.NoError(t, db.Create(call).Error)
	note := &models.Activity{Type: models.ActivityTypeNote, Subject: "Budget note", Notes: "signed off", RelatedType: models.RelatedTypeCompany, RelatedID: company.ID, CreatedByID: admin.ID}
	note.CreatedAt = base.Add(10 * time.Minute)
	require.NoError(t, db.Create(note).Error)

	stage := models.AuditLogEntry{EntityType: "deal", EntityID: deal.ID, Action: "stage_changed",
		Before: models.JSONMap{"stage": "Lead"}, After: models.JSONMap{"stage": "Qualified"}, ActorID: admin.ID, CreatedAt: base.Add(20 * time.Minute)}
	require.NoError(t, db.Create(&stage).Error)
	// Other Deal audit rows (no after.stage) must never leak into the feed.
	require.NoError(t, db.Create(&models.AuditLogEntry{EntityType: "deal", EntityID: deal.ID, Action: "reassigned",
		Before: models.JSONMap{"assigned_to": nil}, After: models.JSONMap{"assigned_to": admin.ID}, ActorID: admin.ID}).Error)

	get := func(t *testing.T, user *models.User, query string, out interface{}) int {
		t.Helper()
		req := testutil.AuthRequest(t, http.MethodGet, "/api/v1/activities?"+query, nil, user.ID, user.Role)
		return doJSON(t, app, req, out).StatusCode
	}

	t.Run("plain list keeps its shape; related_type alone and search work", func(t *testing.T) {
		var out pagedFeed
		require.Equal(t, http.StatusOK, get(t, admin, "related_type=company", &out))
		require.Len(t, out.Data, 1)
		assert.Equal(t, note.ID, out.Data[0].ID)
		assert.Empty(t, out.Data[0].Kind, "no kind field without include_stage_changes")

		require.Equal(t, http.StatusOK, get(t, admin, "search=redesign", &out), "linked deal title")
		require.Len(t, out.Data, 1)
		assert.Equal(t, call.ID, out.Data[0].ID)

		require.Equal(t, http.StatusOK, get(t, admin, "search=SIGNED", &out), "notes")
		require.Len(t, out.Data, 1)
		assert.Equal(t, note.ID, out.Data[0].ID)

		assert.Equal(t, http.StatusBadRequest, get(t, admin, "related_id=1", nil))
	})

	t.Run("feed interleaves stage changes newest first, and pages", func(t *testing.T) {
		var out pagedFeed
		require.Equal(t, http.StatusOK, get(t, admin, "include_stage_changes=true", &out))
		assert.EqualValues(t, 3, out.Total)
		require.Len(t, out.Data, 3)
		assert.Equal(t, "stage_change", out.Data[0].Kind)
		assert.Equal(t, "stage_change", out.Data[0].Type)
		assert.Equal(t, "Lead", out.Data[0].FromStage)
		assert.Equal(t, "Qualified", out.Data[0].ToStage)
		assert.Equal(t, deal.ID, out.Data[0].RelatedID)
		assert.Equal(t, "deal", out.Data[0].RelatedType)
		assert.NotEmpty(t, out.Data[0].CreatedBy)
		assert.Equal(t, []string{"activity", "activity"}, []string{out.Data[1].Kind, out.Data[2].Kind})
		assert.Equal(t, note.ID, out.Data[1].ID)

		require.Equal(t, http.StatusOK, get(t, admin, "include_stage_changes=true&per_page=2&page=2", &out))
		assert.EqualValues(t, 3, out.Total)
		require.Len(t, out.Data, 1)
		assert.Equal(t, call.ID, out.Data[0].ID)
	})

	t.Run("feed filters apply to both halves", func(t *testing.T) {
		var out pagedFeed
		require.Equal(t, http.StatusOK, get(t, admin, "include_stage_changes=true&type=stage_change", &out))
		require.Len(t, out.Data, 1)
		assert.Equal(t, "stage_change", out.Data[0].Kind)

		require.Equal(t, http.StatusOK, get(t, admin, "include_stage_changes=true&type=call", &out))
		require.Len(t, out.Data, 1)
		assert.Equal(t, call.ID, out.Data[0].ID)

		require.Equal(t, http.StatusOK, get(t, admin, "include_stage_changes=true&related_type=company", &out))
		require.Len(t, out.Data, 1)
		assert.Equal(t, note.ID, out.Data[0].ID)

		require.Equal(t, http.StatusOK, get(t, admin, "include_stage_changes=true&search=qualified", &out))
		require.Len(t, out.Data, 1)
		assert.Equal(t, "stage_change", out.Data[0].Kind)
	})

	t.Run("a role outside the audit-log gate gets the plain list", func(t *testing.T) {
		production := testutil.CreateUser(t, db, models.RoleProduction)
		var out pagedFeed
		require.Equal(t, http.StatusOK, get(t, production, "include_stage_changes=true", &out))
		assert.EqualValues(t, 2, out.Total)
		for _, item := range out.Data {
			assert.Empty(t, item.Kind)
		}
	})
}

func TestAuditLogList_HonorsActionFilter(t *testing.T) {
	app, db := testutil.App(t)
	admin := testutil.CreateUser(t, db, models.RoleAdmin)
	deal := seedDeal(t, db, nil)
	require.NoError(t, db.Create(&models.AuditLogEntry{EntityType: "deal", EntityID: deal.ID, Action: "stage_changed",
		Before: models.JSONMap{"stage": "Lead"}, After: models.JSONMap{"stage": "Won"}, ActorID: admin.ID}).Error)
	require.NoError(t, db.Create(&models.AuditLogEntry{EntityType: "deal", EntityID: deal.ID, Action: "reassigned",
		After: models.JSONMap{"assigned_to": admin.ID}, ActorID: admin.ID}).Error)

	var out struct {
		Data []models.AuditLogEntry `json:"data"`
	}
	req := testutil.AuthRequest(t, http.MethodGet, "/api/v1/audit-log?entity_type=deal&action=stage_changed", nil, admin.ID, admin.Role)
	resp := doJSON(t, app, req, &out)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Len(t, out.Data, 1)
	assert.Equal(t, "stage_changed", out.Data[0].Action)
}
