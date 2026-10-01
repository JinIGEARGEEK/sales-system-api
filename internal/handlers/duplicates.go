package handlers

import (
	"slices"
	"strings"

	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"

	"github.com/igeargeek/sales-system-api/internal/utils"
)

// maxDuplicateIDs caps duplicate_of; the client only needs a few to link to.
const maxDuplicateIDs = 10

// rejectDuplicate is the duplicate check Lead, Prospect and Contact Create
// share. It looks for non-deleted rows of model with the same email
// (case-insensitive) or phone (utils.NormalizePhone) and, on a match, writes
// a 409 (utils.DuplicateConflict) naming the matching fields and rows.
// ?allow_duplicate=true skips the check, for a user who has seen the 409 and
// means to create the record anyway.
//
// Returns utils.ErrHandled once a response is written — the caller should
// `return nil`, like the validateX helpers.
func rejectDuplicate(c *fiber.Ctx, db *gorm.DB, model interface{}, noun, email, phone string) error {
	if c.QueryBool("allow_duplicate") {
		return nil
	}
	fields := map[string][]string{}
	var ids []uint

	if e := utils.NormalizeEmail(email); e != "" {
		var found []uint
		if err := db.Model(model).Where("LOWER(TRIM(email)) = ?", e).
			Order("id").Limit(maxDuplicateIDs).Pluck("id", &found).Error; err != nil {
			_ = utils.Internal(c, "Failed to check for duplicates")
			return utils.ErrHandled
		}
		if len(found) > 0 {
			fields["email"] = []string{"duplicate"}
			ids = append(ids, found...)
		}
	}
	if p := utils.NormalizePhone(phone); p != "" {
		var found []uint
		if err := db.Model(model).Where("phone <> '' AND "+utils.NormalizedPhoneSQL("phone")+" = ?", p).
			Order("id").Limit(maxDuplicateIDs).Pluck("id", &found).Error; err != nil {
			_ = utils.Internal(c, "Failed to check for duplicates")
			return utils.ErrHandled
		}
		if len(found) > 0 {
			fields["phone"] = []string{"duplicate"}
			ids = append(ids, found...)
		}
	}
	if len(ids) == 0 {
		return nil
	}

	slices.Sort(ids)
	ids = slices.Compact(ids)
	if len(ids) > maxDuplicateIDs {
		ids = ids[:maxDuplicateIDs]
	}
	matched := make([]string, 0, 2)
	for _, f := range []string{"email", "phone"} {
		if _, ok := fields[f]; ok {
			matched = append(matched, f)
		}
	}
	msg := "A " + noun + " with the same " + strings.Join(matched, " and ") + " already exists"
	_ = utils.DuplicateConflict(c, msg, fields, ids)
	return utils.ErrHandled
}
