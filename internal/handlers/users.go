package handlers

import (
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
		like := "%" + search + "%"
		query = query.Where("first_name ILIKE ? OR last_name ILIKE ? OR email ILIKE ?", like, like, like)
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
// utils.AllowedEmailDomain, nil otherwise. Previously returned
// ValidationError's own result directly, which is nil even on the invalid
// path (the JSON write itself succeeds) — that silently defeated both call
// sites' `if err != nil { return err }` guard below, letting any email
// through regardless of domain.
func validateCompanyEmail(c *fiber.Ctx, email string) error {
	if utils.IsValidCompanyEmail(email) {
		return nil
	}
	msg := "email must be a valid @" + utils.AllowedEmailDomain + " address"
	_ = utils.ValidationError(c, msg, map[string][]string{"email": {msg}})
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
// @Failure 400 {object} map[string]interface{} "Invalid body or missing required fields"
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

	if err := h.DB.Create(&user).Error; err != nil {
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
	if err := h.DB.First(&user, c.Params("id")).Error; err != nil {
		return utils.NotFound(c, "User not found")
	}
	return utils.OK(c, user)
}

// Update godoc
// @Summary Update a user (Admin only)
// @Description Admin only. Full update; email must be on the allowed company domain. Password is only changed if provided (forces must_change_password true). The response never includes password_hash (excluded via json:"-").
// @Tags users
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param id path int true "User ID"
// @Param body body userForm true "User fields"
// @Success 200 {object} models.User
// @Failure 400 {object} map[string]interface{} "Invalid body or missing required fields"
// @Failure 404 {object} map[string]interface{} "User not found"
// @Router /users/{id} [put]
func (h *UserHandler) Update(c *fiber.Ctx) error {
	var user models.User
	if err := h.DB.First(&user, c.Params("id")).Error; err != nil {
		return utils.NotFound(c, "User not found")
	}

	var form userForm
	if err := c.BodyParser(&form); err != nil {
		return utils.BadRequest(c, "Invalid request body")
	}
	if err := utils.RequireFields(c, utils.Field{Name: "email", Value: form.Email}); err != nil {
		return err
	}
	if err := validateCompanyEmail(c, form.Email); err != nil {
		return nil
	}

	actorID := middleware.CurrentUserID(c)
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

	if err := h.DB.Save(&user).Error; err != nil {
		return utils.Internal(c, "Failed to update user")
	}
	middleware.InvalidateMustChangePassword(user.ID)
	// IsActive may have just flipped to false (or a password reset above
	// should force existing sessions to re-authenticate) — drop the cached
	// auth state so RequireAuth re-reads it on this user's very next request
	// rather than up to authCacheTTL later.
	middleware.InvalidateAuthCache(user.ID)
	return utils.OK(c, user)
}

// Delete godoc
// @Summary Delete a user (Admin only)
// @Description Admin only. Soft-delete (deactivates and gorm soft-deletes the row), not a hard delete §1.6. Also invalidates the user's cached auth state.
// @Tags users
// @Security BearerAuth
// @Param id path int true "User ID"
// @Success 204 "No Content"
// @Failure 404 {object} map[string]interface{} "User not found"
// @Router /users/{id} [delete]
func (h *UserHandler) Delete(c *fiber.Ctx) error {
	var user models.User
	if err := h.DB.First(&user, c.Params("id")).Error; err != nil {
		return utils.NotFound(c, "User not found")
	}

	actorID := middleware.CurrentUserID(c)
	if err := h.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&user).Updates(map[string]interface{}{
			"is_active":  false,
			"deleted_by": actorID,
		}).Error; err != nil {
			return err
		}
		return tx.Delete(&user).Error
	}); err != nil {
		return utils.Internal(c, "Failed to delete user")
	}
	middleware.InvalidateAuthCache(user.ID)
	return utils.NoContent(c)
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
		})
	}
	return utils.OK(c, members)
}
