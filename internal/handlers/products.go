package handlers

import (
	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"

	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/utils"
)

type ProductHandler struct {
	DB *gorm.DB
}

func NewProductHandler(db *gorm.DB) *ProductHandler {
	return &ProductHandler{DB: db}
}

// List — GET /products (Admin only, route-gated).
func (h *ProductHandler) List(c *fiber.Ctx) error {
	page, perPage, offset := utils.Pagination(c)
	query := h.DB.Model(&models.Product{})

	if v := c.Query("category"); v != "" {
		query = query.Where("category = ?", v)
	}
	if v := c.Query("search"); v != "" {
		query = query.Where("name ILIKE ? OR sku ILIKE ?", "%"+v+"%", "%"+v+"%")
	}

	var total int64
	query.Count(&total)

	var products []models.Product
	query = utils.ApplySort(query, c.Query("sort"), map[string]bool{"created_at": true, "name": true}, "-created_at")
	if err := query.Limit(perPage).Offset(offset).Find(&products).Error; err != nil {
		return utils.Internal(c, "Failed to list products")
	}
	return utils.List(c, products, page, perPage, total)
}

type productForm struct {
	Name        string `json:"name"`
	SKU         string `json:"sku"`
	Category    string `json:"category"`
	Description string `json:"description"`
	IsActive    *bool  `json:"is_active"`
}

// Create — POST /products (Admin only).
func (h *ProductHandler) Create(c *fiber.Ctx) error {
	var form productForm
	if err := c.BodyParser(&form); err != nil {
		return utils.BadRequest(c, "Invalid request body")
	}
	if form.Name == "" {
		return utils.ValidationError(c, "name is required", map[string][]string{"name": {"required"}})
	}

	product := models.Product{Name: form.Name, SKU: form.SKU, Category: form.Category, Description: form.Description, IsActive: true}
	if form.IsActive != nil {
		product.IsActive = *form.IsActive
	}
	if err := h.DB.Create(&product).Error; err != nil {
		return utils.Internal(c, "Failed to create product")
	}
	return utils.Created(c, product)
}

// Deactivate — PATCH /products/:id/deactivate (Admin only). Sets is_active: false.
func (h *ProductHandler) Deactivate(c *fiber.Ctx) error {
	var product models.Product
	if err := h.DB.First(&product, c.Params("id")).Error; err != nil {
		return utils.NotFound(c, "Product not found")
	}
	product.IsActive = false
	if err := h.DB.Save(&product).Error; err != nil {
		return utils.Internal(c, "Failed to deactivate product")
	}
	return utils.OK(c, product)
}

type customerProductResponse struct {
	models.CustomerProduct
	Product models.Product `json:"product"`
}

// ListForCompany — GET /companies/:companyId/products. Lists a Company's
// Customer-Product records with the Product merged in.
func (h *ProductHandler) ListForCompany(c *fiber.Ctx) error {
	var records []models.CustomerProduct
	if err := h.DB.Where("company_id = ?", c.Params("companyId")).Find(&records).Error; err != nil {
		return utils.Internal(c, "Failed to list customer products")
	}

	productIDs := make([]uint, 0, len(records))
	for _, r := range records {
		productIDs = append(productIDs, r.ProductID)
	}
	var products []models.Product
	if len(productIDs) > 0 {
		h.DB.Where("id IN ?", productIDs).Find(&products)
	}
	productByID := make(map[uint]models.Product, len(products))
	for _, p := range products {
		productByID[p.ID] = p
	}

	result := make([]customerProductResponse, 0, len(records))
	for _, r := range records {
		result = append(result, customerProductResponse{CustomerProduct: r, Product: productByID[r.ProductID]})
	}
	return utils.OK(c, result)
}

type customerProductForm struct {
	ProductID    uint                         `json:"product_id"`
	Status       models.CustomerProductStatus `json:"status"`
	StartDate    *string                      `json:"start_date"`
	EndDate      *string                      `json:"end_date"`
	SourceDealID *uint                        `json:"source_deal_id"`
}

// AddForCompany — POST /companies/:companyId/products. Manually add/change
// status independent of a Deal — FR-CRM-065.
func (h *ProductHandler) AddForCompany(c *fiber.Ctx) error {
	var company models.Company
	if err := h.DB.First(&company, c.Params("companyId")).Error; err != nil {
		return utils.NotFound(c, "Company not found")
	}

	var form customerProductForm
	if err := c.BodyParser(&form); err != nil {
		return utils.BadRequest(c, "Invalid request body")
	}
	if form.ProductID == 0 {
		return utils.ValidationError(c, "product_id is required", map[string][]string{"product_id": {"required"}})
	}

	record := models.CustomerProduct{
		CompanyID: company.ID, ProductID: form.ProductID, Status: form.Status, SourceDealID: form.SourceDealID,
	}
	if record.Status == "" {
		record.Status = models.CustomerProductInterested
	}
	if err := h.DB.Create(&record).Error; err != nil {
		return utils.Internal(c, "Failed to create customer product")
	}
	return utils.Created(c, record)
}
