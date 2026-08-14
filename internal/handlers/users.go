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

// List — GET /users (Admin). Filters: role, status (active/inactive), search (name/email).
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

type userForm struct {
	FirstName string      `json:"first_name"`
	LastName  string      `json:"last_name"`
	Email     string      `json:"email"`
	Tel       string      `json:"tel"`
	Username  string      `json:"username"`
	Password  string      `json:"password"`
	Role      models.Role `json:"role"`
	Status    string      `json:"status"`
	Notes     string      `json:"notes"`
}

// Create — POST /users (Admin). Body per AdminUserForm: first_name, last_name,
// email, tel, role, status, notes.
func (h *UserHandler) Create(c *fiber.Ctx) error {
	var form userForm
	if err := c.BodyParser(&form); err != nil {
		return utils.BadRequest(c, "Invalid request body")
	}
	if form.FirstName == "" || form.Email == "" {
		return utils.ValidationError(c, "first_name and email are required", map[string][]string{
			"first_name": {"required"},
			"email":      {"required"},
		})
	}

	username := form.Username
	if username == "" {
		username = form.Email
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
		FirstName:    form.FirstName,
		LastName:     form.LastName,
		Email:        form.Email,
		Tel:          form.Tel,
		Username:     username,
		PasswordHash: hash,
		Role:         form.Role,
		Notes:        form.Notes,
		IsActive:     form.Status != "inactive",
	}
	user.CreatedBy = &actorID
	user.UpdatedBy = &actorID

	if err := h.DB.Create(&user).Error; err != nil {
		return utils.ValidationError(c, "Email or username already in use", map[string][]string{
			"email": {"Email is already in use"},
		})
	}
	return utils.Created(c, user)
}

// Get — GET /users/:id (Admin).
func (h *UserHandler) Get(c *fiber.Ctx) error {
	var user models.User
	if err := h.DB.First(&user, c.Params("id")).Error; err != nil {
		return utils.NotFound(c, "User not found")
	}
	return utils.OK(c, user)
}

// Update — PUT /users/:id (Admin). Full update.
func (h *UserHandler) Update(c *fiber.Ctx) error {
	var user models.User
	if err := h.DB.First(&user, c.Params("id")).Error; err != nil {
		return utils.NotFound(c, "User not found")
	}

	var form userForm
	if err := c.BodyParser(&form); err != nil {
		return utils.BadRequest(c, "Invalid request body")
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
	}

	if err := h.DB.Save(&user).Error; err != nil {
		return utils.Internal(c, "Failed to update user")
	}
	return utils.OK(c, user)
}

// Delete — DELETE /users/:id (Admin). Soft-delete (deactivate), not a hard delete §1.6.
func (h *UserHandler) Delete(c *fiber.Ctx) error {
	var user models.User
	if err := h.DB.First(&user, c.Params("id")).Error; err != nil {
		return utils.NotFound(c, "User not found")
	}

	actorID := middleware.CurrentUserID(c)
	if err := h.DB.Model(&user).Updates(map[string]interface{}{
		"is_active":  false,
		"deleted_by": actorID,
	}).Error; err != nil {
		return utils.Internal(c, "Failed to deactivate user")
	}
	if err := h.DB.Delete(&user).Error; err != nil {
		return utils.Internal(c, "Failed to delete user")
	}
	return utils.NoContent(c)
}

type teamMember struct {
	ID    uint   `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

// TeamMembers — GET /team-members (any authenticated). Lightweight list for
// assignee dropdowns — no Admin role required, §2.2.
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
