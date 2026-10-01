package handlers

import (
	"errors"
	"slices"
	"strconv"

	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"

	"github.com/igeargeek/sales-system-api/internal/middleware"
	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/utils"
)

type UserHandler struct {
	DB *gorm.DB
}

func NewUserHandler(db *gorm.DB) *UserHandler {
	return &UserHandler{DB: db}
}

// List godoc
// @Summary List users (Admin only)
// @Description Returns a paginated list of users. Admin only. Filters: role, status (active/inactive), search (matches first_name/last_name/email). Sortable by created_at, first_name, email.
// @Tags users
// @Security BearerAuth
// @Produce json
// @Param role query string false "Filter by role"
// @Param status query string false "Filter by status (active/inactive)"
// @Param search query string false "Search first_name/last_name/email"
// @Param sort query string false "Sort field, prefix with - for descending (default -created_at)"
// @Success 200 {object} map[string]interface{} "Paginated user list (data, page, per_page, total)"
// @Router /users [get]
func (h *UserHandler) List(c *fiber.Ctx) error {
	page, perPage, offset := utils.Pagination(c)
	query := h.DB.Model(&models.User{})

	if role := c.Query("role"); role != "" {
		query = query.Where("role = ?", role)
	}
	if status := c.Query("status"); status != "" {
		query = query.Where("is_active = ?", status == "active")
	}
	if search := c.Query("search"); search != "" {
		like := utils.LikePattern(search)
		query = query.Where("first_name ILIKE ? ESCAPE '\\' OR last_name ILIKE ? ESCAPE '\\' OR email ILIKE ? ESCAPE '\\'", like, like, like)
	}

	var total int64
	query.Count(&total)

	var users []models.User
	query = utils.ApplySort(query, c.Query("sort"), map[string]bool{"created_at": true, "first_name": true, "email": true}, "-created_at")
	if err := query.Limit(perPage).Offset(offset).Find(&users).Error; err != nil {
		return utils.Internal(c, "Failed to list users")
	}

	return utils.List(c, users, page, perPage, total)
}

// validateCompanyEmail returns utils.ErrHandled (see its doc) after writing a
// 422 ValidationError if email isn't a valid address on
// utils.AllowedEmailDomain, nil otherwise.
func validateCompanyEmail(c *fiber.Ctx, email string) error {
	if utils.IsValidCompanyEmail(email) {
		return nil
	}
	msg := "email must be a valid @" + utils.AllowedEmailDomain + " address"
	_ = utils.ValidationError(c, msg, map[string][]string{"email": {msg}})
	return utils.ErrHandled
}

// validateUserRole mirrors validateCompanyEmail: utils.ErrHandled after
// writing a 422 if role isn't a models.ValidRoles one (anything else would
// store an account no route group recognises), nil otherwise.
func validateUserRole(c *fiber.Ctx, role models.Role) error {
	if models.IsValidRole(role) {
		return nil
	}
	msg := "role must be one of Admin, Sales Rep, Sales Manager, Production, Marketing"
	_ = utils.ValidationError(c, msg, map[string][]string{"role": {msg}})
	return utils.ErrHandled
}

type userForm struct {
	FirstName string      `json:"first_name"`
	LastName  string      `json:"last_name"`
	Email     string      `json:"email"`
	Tel       string      `json:"tel"`
	Password  string      `json:"password"`
	Role      models.Role `json:"role"`
	Status    string      `json:"status"`
	Notes     string      `json:"notes"`
}

// Create godoc
// @Summary Create a user (Admin only)
// @Description Admin only. Creates a user account; email must be on the allowed company domain. If password is omitted, a temporary password is generated and must_change_password is forced true. The response never includes password_hash (excluded via json:"-").
// @Tags users
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param body body userForm true "User fields (first_name, last_name, email, tel, password, role, status, notes)"
// @Success 201 {object} models.User
// @Failure 400 {object} map[string]interface{} "Invalid body"
// @Failure 422 {object} map[string]interface{} "Missing first_name/email, email not on the company domain or already in use, or role not one of Admin/Sales Rep/Sales Manager/Production/Marketing"
// @Router /users [post]
func (h *UserHandler) Create(c *fiber.Ctx) error {
	var form userForm
	if err := c.BodyParser(&form); err != nil {
		return utils.BadRequest(c, "Invalid request body")
	}
	if err := utils.RequireFields(c,
		utils.Field{Name: "first_name", Value: form.FirstName},
		utils.Field{Name: "email", Value: form.Email},
	); err != nil {
		return err
	}
	if err := validateCompanyEmail(c, form.Email); err != nil {
		return nil
	}
	if err := validateUserRole(c, form.Role); err != nil {
		return nil
	}

	password := form.Password
	if password == "" {
		password = utils.NewTempPassword()
	}
	hash, err := utils.HashPassword(password)
	if err != nil {
		return utils.Internal(c, "Failed to hash password")
	}

	actorID := middleware.CurrentUserID(c)
	user := models.User{
		FirstName:          form.FirstName,
		LastName:           form.LastName,
		Email:              form.Email,
		Tel:                form.Tel,
		PasswordHash:       hash,
		Role:               form.Role,
		Notes:              form.Notes,
		IsActive:           form.Status != "inactive",
		MustChangePassword: true,
	}
	user.CreatedBy = &actorID
	user.UpdatedBy = &actorID

	// CreateKeepingFalse, not Create: is_active is `default:true`, so a
	// plain Create would silently store status "inactive" as active.
	if err := utils.CreateKeepingFalse(h.DB, &user); err != nil {
		return utils.ValidationError(c, "Email already in use", map[string][]string{
			"email": {"Email already in use"},
		})
	}
	return utils.Created(c, user)
}

// Get godoc
// @Summary Get a user (Admin only)
// @Description Admin only. Returns a single user by ID. The response never includes password_hash (excluded via json:"-").
// @Tags users
// @Security BearerAuth
// @Produce json
// @Param id path int true "User ID"
// @Success 200 {object} models.User
// @Failure 404 {object} map[string]interface{} "User not found"
// @Router /users/{id} [get]
func (h *UserHandler) Get(c *fiber.Ctx) error {
	var user models.User
	if err := utils.FindByID(c, h.DB, &user, "User not found"); err != nil {
		return nil
	}
	return utils.OK(c, user)
}

// userUpdateForm is PUT /users/:id's body: userForm plus reassign_to.
type userUpdateForm struct {
	userForm
	// ReassignTo receives the user's open records when this update takes
	// them away (deactivation, or a move to a role that can't own pipeline
	// records). Ignored otherwise.
	ReassignTo *uint `json:"reassign_to"`
}

// userWriteResponse is PUT /users/:id's response: the user plus, when the
// update took their records away from them, the open records they still
// own (open_records) and any reassign_to hand-off (reassigned).
type userWriteResponse struct {
	models.User
	OpenRecords *openRecordCounts `json:"open_records,omitempty"`
	Reassigned  *reassignResult   `json:"reassigned,omitempty"`
}

// Update godoc
// @Summary Update a user (Admin only)
// @Description Admin only. Full update; email must be on the allowed company domain and role one of the known roles. Password is only changed if provided (forces must_change_password true). Changing role, resetting the password, or deactivating signs the user out of every existing session (their tokens stop working immediately). The caller cannot change their own role or deactivate themselves (422), and the last active Admin cannot be demoted or deactivated (409). Role and is_active changes write role_changed/activated/deactivated audit entries. When the update deactivates the user or moves them to a role that can't own pipeline records (Production), the response adds open_records (their remaining open Deals/Leads/Prospects/pending Tasks); an optional reassign_to (an active sales-role user) first moves those records to that user in the same transaction, reported in reassigned. The response never includes password_hash (excluded via json:"-").
// @Tags users
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param id path int true "User ID"
// @Param body body userUpdateForm true "User fields, plus optional reassign_to"
// @Success 200 {object} userWriteResponse
// @Failure 400 {object} map[string]interface{} "Invalid body"
// @Failure 404 {object} map[string]interface{} "User not found"
// @Failure 409 {object} map[string]interface{} "Would demote or deactivate the last active Admin"
// @Failure 422 {object} map[string]interface{} "Missing email, email not on the company domain, role not one of Admin/Sales Rep/Sales Manager/Production/Marketing, changing your own role or deactivating yourself, or an invalid reassign_to"
// @Router /users/{id} [put]
func (h *UserHandler) Update(c *fiber.Ctx) error {
	var user models.User
	if err := utils.FindByID(c, h.DB, &user, "User not found"); err != nil {
		return nil
	}

	var form userUpdateForm
	if err := c.BodyParser(&form); err != nil {
		return utils.BadRequest(c, "Invalid request body")
	}
	if err := utils.RequireFields(c, utils.Field{Name: "email", Value: form.Email}); err != nil {
		return err
	}
	if err := validateCompanyEmail(c, form.Email); err != nil {
		return nil
	}
	if err := validateUserRole(c, form.Role); err != nil {
		return nil
	}

	actorID := middleware.CurrentUserID(c)
	roleChanged := form.Role != user.Role
	deactivating := user.IsActive && form.Status == "inactive"
	if user.ID == actorID && (roleChanged || deactivating) {
		if roleChanged {
			return selfAccountError(c, "role")
		}
		return selfAccountError(c, "status")
	}
	losesAdmin := user.IsActive && user.Role == models.RoleAdmin && (roleChanged || deactivating)
	// Their open records need a new owner once they can't work them.
	losesRecords := deactivating ||
		(user.IsActive && ownsPipelineRecords(user.Role) && !ownsPipelineRecords(form.Role))
	reassignTo := form.ReassignTo
	if !losesRecords {
		reassignTo = nil
	}
	if reassignTo != nil {
		if err := validateReassignTo(c, h.DB, *reassignTo, []uint{user.ID}); err != nil {
			return nil
		}
	}

	// Any of these makes the user's existing tokens stale: a token minted
	// under the old role, the old password, or while still active.
	revokeSessions := roleChanged || form.Password != "" || deactivating

	oldRole, wasActive := user.Role, user.IsActive
	user.FirstName = form.FirstName
	user.LastName = form.LastName
	user.Email = form.Email
	user.Tel = form.Tel
	user.Role = form.Role
	user.Notes = form.Notes
	if form.Status != "" {
		user.IsActive = form.Status != "inactive"
	}
	user.UpdatedBy = &actorID

	if form.Password != "" {
		hash, err := utils.HashPassword(form.Password)
		if err != nil {
			return utils.Internal(c, "Failed to hash password")
		}
		user.PasswordHash = hash
		user.MustChangePassword = true
	}

	var moved openRecordCounts
	err := h.DB.Transaction(func(tx *gorm.DB) error {
		if losesAdmin {
			if err := guardLastAdmin(tx, []uint{user.ID}); err != nil {
				return err
			}
		}
		if err := tx.Save(&user).Error; err != nil {
			return err
		}
		if revokeSessions {
			if err := bumpTokenVersion(tx, &user); err != nil {
				return err
			}
		}
		if roleChanged {
			if err := utils.WriteAuditLog(tx, "user", user.ID, "role_changed",
				models.JSONMap{"role": oldRole}, models.JSONMap{"role": user.Role}, actorID); err != nil {
				return err
			}
		}
		if wasActive != user.IsActive {
			action := "deactivated"
			if user.IsActive {
				action = "activated"
			}
			if err := utils.WriteAuditLog(tx, "user", user.ID, action,
				models.JSONMap{"is_active": wasActive}, models.JSONMap{"is_active": user.IsActive}, actorID); err != nil {
				return err
			}
		}
		if reassignTo != nil {
			var err error
			moved, err = reassignOpenRecords(tx, user.ID, *reassignTo, actorID)
			return err
		}
		return nil
	})
	if errors.Is(err, errLastAdmin) {
		return utils.Conflict(c, "Cannot demote or deactivate the last active Admin")
	}
	if err != nil {
		return utils.Internal(c, "Failed to update user")
	}
	middleware.InvalidateMustChangePassword(user.ID)
	// is_active, role, or token_version may have just changed — drop the
	// cached auth state so RequireAuth re-reads it on this user's very next
	// request rather than up to authCacheTTL later.
	middleware.InvalidateAuthCache(user.ID)

	resp := userWriteResponse{User: user}
	if losesRecords {
		// The update is committed; a failed count only drops the hint.
		if open, err := countOpenRecords(h.DB, user.ID); err == nil {
			resp.OpenRecords = &open
		}
		if reassignTo != nil {
			resp.Reassigned = &reassignResult{UserID: user.ID, ReassignTo: *reassignTo, openRecordCounts: moved}
		}
	}
	return utils.OK(c, resp)
}

// userDeleteForm is DELETE /users/:id's optional JSON body.
type userDeleteForm struct {
	ReassignTo *uint `json:"reassign_to"`
}

// userDeleteResponse is DELETE /users/:id's response — see Delete's doc.
type userDeleteResponse struct {
	ID          uint             `json:"id"`
	OpenRecords openRecordCounts `json:"open_records"`
	Reassigned  *reassignResult  `json:"reassigned,omitempty"`
}

// deleteReassignTo reads DELETE /users/:id's optional reassign_to from the
// query string (?reassign_to=) or a JSON body, since some HTTP clients drop
// DELETE bodies. Writes a 400/422 and returns utils.ErrHandled on a bad value.
func deleteReassignTo(c *fiber.Ctx) (*uint, error) {
	if raw := c.Query("reassign_to"); raw != "" {
		n, err := strconv.ParseUint(raw, 10, 32)
		if err != nil || n == 0 {
			_ = utils.ValidationError(c, "reassign_to must be a user id", map[string][]string{"reassign_to": {"invalid"}})
			return nil, utils.ErrHandled
		}
		id := uint(n)
		return &id, nil
	}
	if len(c.Body()) == 0 {
		return nil, nil
	}
	var form userDeleteForm
	if err := c.BodyParser(&form); err != nil {
		_ = utils.BadRequest(c, "Invalid request body")
		return nil, utils.ErrHandled
	}
	return form.ReassignTo, nil
}

// Delete godoc
// @Summary Delete a user (Admin only)
// @Description Admin only. Soft-delete (deactivates and gorm soft-deletes the row), not a hard delete §1.6. Also invalidates the user's cached auth state. The caller cannot delete themselves (422), nor the last active Admin (409). Optional reassign_to (query ?reassign_to= or JSON body; an active sales-role user) moves the user's open Deals/Leads/Prospects/pending Tasks to that user in the same transaction. Returns 200 with open_records (open records the deleted user still owns) and, with reassign_to, reassigned (what moved).
// @Tags users
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param id path int true "User ID"
// @Param reassign_to query int false "User to receive the deleted user's open records"
// @Param body body userDeleteForm false "Optional reassign_to"
// @Success 200 {object} userDeleteResponse
// @Failure 404 {object} map[string]interface{} "User not found"
// @Failure 409 {object} map[string]interface{} "Would delete the last active Admin"
// @Failure 422 {object} map[string]interface{} "Deleting yourself, or an invalid reassign_to"
// @Router /users/{id} [delete]
func (h *UserHandler) Delete(c *fiber.Ctx) error {
	var user models.User
	if err := utils.FindByID(c, h.DB, &user, "User not found"); err != nil {
		return nil
	}

	actorID := middleware.CurrentUserID(c)
	if user.ID == actorID {
		return selfAccountError(c, "id")
	}
	reassignTo, err := deleteReassignTo(c)
	if err != nil {
		return nil
	}
	if reassignTo != nil {
		if err := validateReassignTo(c, h.DB, *reassignTo, []uint{user.ID}); err != nil {
			return nil
		}
	}

	var moved openRecordCounts
	err = h.DB.Transaction(func(tx *gorm.DB) error {
		if user.IsActive && user.Role == models.RoleAdmin {
			if err := guardLastAdmin(tx, []uint{user.ID}); err != nil {
				return err
			}
		}
		if err := tx.Model(&user).Updates(map[string]interface{}{
			"is_active":  false,
			"deleted_by": actorID,
		}).Error; err != nil {
			return err
		}
		// Revoke sessions too, so a later Restore + re-activate doesn't
		// bring this user's pre-delete tokens back to life.
		if err := bumpTokenVersion(tx, &user); err != nil {
			return err
		}
		if reassignTo != nil {
			var err error
			if moved, err = reassignOpenRecords(tx, user.ID, *reassignTo, actorID); err != nil {
				return err
			}
		}
		return tx.Delete(&user).Error
	})
	if errors.Is(err, errLastAdmin) {
		return utils.Conflict(c, "Cannot delete the last active Admin")
	}
	if err != nil {
		return utils.Internal(c, "Failed to delete user")
	}
	middleware.InvalidateAuthCache(user.ID)

	resp := userDeleteResponse{ID: user.ID}
	// The delete is committed; a failed count only leaves zeros.
	resp.OpenRecords, _ = countOpenRecords(h.DB, user.ID)
	if reassignTo != nil {
		resp.Reassigned = &reassignResult{UserID: user.ID, ReassignTo: *reassignTo, openRecordCounts: moved}
	}
	return utils.OK(c, resp)
}

// bulkUserIDsForm is bulk-activate's body.
type bulkUserIDsForm struct {
	IDs []uint `json:"ids"`
}

// bulkDeactivateUsersForm is bulk-deactivate's body: the ids plus an
// optional reassign_to that receives every listed user's open records.
// bulkSetActive parses it for both endpoints; activate ignores reassign_to.
type bulkDeactivateUsersForm struct {
	bulkUserIDsForm
	ReassignTo *uint `json:"reassign_to"`
}

// bulkDeactivateResponse is bulk-deactivate's response: each listed user's
// remaining open records, and what reassign_to moved (empty without it).
type bulkDeactivateResponse struct {
	OpenRecords []userOpenRecords `json:"open_records"`
	Reassigned  []reassignResult  `json:"reassigned"`
}

// bulkSetActive is the shared implementation behind BulkActivate/
// BulkDeactivate — same "loop over ids, mutate, save, audit" shape as
// bulk_ops.go's bulkArchiveEntity, but without a per-row CanWrite check:
// User has no AssignedTo/owner field to check against, and the whole /users
// route group is already Admin-only (routes.go's adminOnly), unlike
// Deal/Lead/Prospect's bulk endpoints which sit behind the broader
// Admin-or-Sales-Manager bulkRoles. Deactivating also applies Update's
// self/last-Admin guards and optional reassign_to.
func (h *UserHandler) bulkSetActive(c *fiber.Ctx, active bool, action string, failMsg string) error {
	var form bulkDeactivateUsersForm
	if err := c.BodyParser(&form); err != nil {
		return utils.BadRequest(c, "Invalid request body")
	}
	if !utils.ValidateBulkIDCount(c, form.IDs) {
		return nil
	}

	actorID := middleware.CurrentUserID(c)
	ids := utils.DedupeUints(form.IDs)
	reassignTo := form.ReassignTo
	if active {
		reassignTo = nil
	} else {
		if slices.Contains(ids, actorID) {
			return selfAccountError(c, "ids")
		}
		if reassignTo != nil {
			if err := validateReassignTo(c, h.DB, *reassignTo, ids); err != nil {
				return nil
			}
		}
	}

	reassigned := []reassignResult{}
	err := h.DB.Transaction(func(tx *gorm.DB) error {
		if !active {
			if err := guardLastAdmin(tx, ids); err != nil {
				return err
			}
		}
		if err := utils.BulkUpdate(tx, ids, "user", action, actorID,
			func(tx *gorm.DB, item *models.User) (models.JSONMap, models.JSONMap, error) {
				before := models.JSONMap{"is_active": item.IsActive}
				wasActive := item.IsActive
				item.IsActive = active
				after := models.JSONMap{"is_active": item.IsActive}
				if err := tx.Save(item).Error; err != nil {
					return before, after, err
				}
				// Same as Update: deactivating revokes existing sessions, so a
				// later re-activation doesn't bring the old tokens back to life.
				if wasActive && !active {
					return before, after, bumpTokenVersion(tx, item)
				}
				return before, after, nil
			}); err != nil {
			return err
		}
		if reassignTo == nil {
			return nil
		}
		for _, id := range ids {
			moved, err := reassignOpenRecords(tx, id, *reassignTo, actorID)
			if err != nil {
				return err
			}
			if moved.Total > 0 {
				reassigned = append(reassigned, reassignResult{UserID: id, ReassignTo: *reassignTo, openRecordCounts: moved})
			}
		}
		return nil
	})
	if errors.Is(err, errLastAdmin) {
		return utils.Conflict(c, "Cannot deactivate the last active Admin")
	}
	if err != nil {
		return utils.Internal(c, failMsg)
	}
	// Same reason as Update/Delete above — is_active (or a still-cached
	// stale value of it) gates RequireAuth, so drop the cache for every
	// affected user rather than waiting up to authCacheTTL.
	for _, id := range ids {
		middleware.InvalidateAuthCache(id)
	}
	if active {
		return utils.NoContent(c)
	}

	resp := bulkDeactivateResponse{OpenRecords: make([]userOpenRecords, 0, len(ids)), Reassigned: reassigned}
	for _, id := range ids {
		// Committed already; a failed count only drops that user's hint.
		if open, err := countOpenRecords(h.DB, id); err == nil {
			resp.OpenRecords = append(resp.OpenRecords, userOpenRecords{UserID: id, openRecordCounts: open})
		}
	}
	return utils.OK(c, resp)
}

// BulkActivate godoc
// @Summary Bulk activate users (Admin only)
// @Description Sets is_active true for every listed user in one transaction, writing a bulk_activated audit entry per row.
// @Tags users
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param body body bulkUserIDsForm true "User IDs"
// @Success 204 "No Content"
// @Failure 400 {object} map[string]interface{} "Invalid request body, or ids is required"
// @Router /users/bulk-activate [patch]
func (h *UserHandler) BulkActivate(c *fiber.Ctx) error {
	return h.bulkSetActive(c, true, "bulk_activated", "Failed to bulk activate users")
}

// BulkDeactivate godoc
// @Summary Bulk deactivate users (Admin only)
// @Description Sets is_active false for every listed user in one transaction (same effect as Delete's is_active flip, without the soft-delete), writing a bulk_deactivated audit entry per row. Revokes every deactivated user's existing sessions, so re-activating them later does not revive old tokens. The caller cannot be in ids (422) and the last active Admin cannot be deactivated (409). Optional reassign_to (an active sales-role user, not in ids) moves every listed user's open Deals/Leads/Prospects/pending Tasks to that user in the same transaction. Returns open_records per listed user (what they still own) and reassigned (what moved, per user).
// @Tags users
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param body body bulkDeactivateUsersForm true "User IDs, plus optional reassign_to"
// @Success 200 {object} bulkDeactivateResponse
// @Failure 400 {object} map[string]interface{} "Invalid request body, or ids is required"
// @Failure 409 {object} map[string]interface{} "Would deactivate the last active Admin"
// @Failure 422 {object} map[string]interface{} "ids includes the caller, or an invalid reassign_to"
// @Router /users/bulk-deactivate [patch]
func (h *UserHandler) BulkDeactivate(c *fiber.Ctx) error {
	return h.bulkSetActive(c, false, "bulk_deactivated", "Failed to bulk deactivate users")
}

// Trash godoc
// @Summary List deleted users (Admin only)
// @Description Admin only. Returns soft-deleted users.
// @Tags users
// @Security BearerAuth
// @Produce json
// @Success 200 {object} map[string]interface{} "Paginated user list (data, page, per_page, total)"
// @Router /users/trash [get]
func (h *UserHandler) Trash(c *fiber.Ctx) error {
	return utils.GenericTrash[models.User](c, h.DB, "Failed to list deleted users")
}

// Restore godoc
// @Summary Restore a deleted user (Admin only)
// @Description Admin only. Restores a soft-deleted user. Leaves is_active false — an Admin re-activates separately via Update.
// @Tags users
// @Security BearerAuth
// @Produce json
// @Param id path int true "User ID"
// @Success 200 {object} models.User
// @Failure 404 {object} map[string]interface{} "Deleted user not found"
// @Router /users/{id}/restore [post]
func (h *UserHandler) Restore(c *fiber.Ctx) error {
	return utils.GenericRestore[models.User](c, h.DB, "Deleted user not found", "Failed to restore user")
}

type teamMember struct {
	ID    uint   `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
	// Role lets pickers offer only users who may own the record (e.g. no
	// Production on Deals/Tasks — validateAssignee).
	Role models.Role `json:"role"`
}

// TeamMembers godoc
// @Summary List team members (any authenticated role)
// @Description Any authenticated role — not Admin-restricted, §2.2. Lightweight active-user list (id, name, email) for assignee dropdowns.
// @Tags users
// @Security BearerAuth
// @Produce json
// @Success 200 {array} teamMember
// @Router /team-members [get]
func (h *UserHandler) TeamMembers(c *fiber.Ctx) error {
	var users []models.User
	if err := h.DB.Where("is_active = ?", true).Find(&users).Error; err != nil {
		return utils.Internal(c, "Failed to list team members")
	}

	members := make([]teamMember, 0, len(users))
	for _, u := range users {
		members = append(members, teamMember{
			ID:    u.ID,
			Name:  u.FirstName + " " + u.LastName,
			Email: u.Email,
			Role:  u.Role,
		})
	}
	return utils.OK(c, members)
}
