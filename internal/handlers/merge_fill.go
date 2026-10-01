package handlers

import (
	"strings"

	"github.com/lib/pq"

	"github.com/igeargeek/sales-system-api/internal/models"
	"github.com/igeargeek/sales-system-api/internal/utils"
)

// merge_fill.go — the field half of a Company/Contact merge (merge.go). The
// target keeps every non-empty field; an empty one takes the first non-empty
// value among the sources, in source_ids order. Tags are the union, through
// normalizeTags (lowercased, trimmed, deduped). Fields that identify the
// record are never overwritten: a source with a different non-empty value is
// reported as a conflict instead.

func blank(s string) bool { return strings.TrimSpace(s) == "" }

func blankPtr(s *string) bool { return s == nil || blank(*s) }

// fillString sets *dst to the first non-blank value when *dst is blank.
func fillString(dst *string, name string, filled *[]string, values []string) {
	if !blank(*dst) {
		return
	}
	for _, v := range values {
		if !blank(v) {
			*dst = v
			*filled = append(*filled, name)
			return
		}
	}
}

// fillPtr is fillString for a nullable column.
func fillPtr(dst **string, name string, filled *[]string, values []*string) {
	if !blankPtr(*dst) {
		return
	}
	for _, v := range values {
		if !blankPtr(v) {
			s := *v
			*dst = &s
			*filled = append(*filled, name)
			return
		}
	}
}

// sourceValues reads one field from each source, in source_ids order.
func sourceValues[T, V any](sources []*T, get func(*T) V) []V {
	out := make([]V, len(sources))
	for i, s := range sources {
		out[i] = get(s)
	}
	return out
}

// mergeSourceTags sets *dst to the union of its tags and every source's,
// adding "tags" to filled when that added any.
func mergeSourceTags[T any](dst *pq.StringArray, sources []*T, tags func(*T) pq.StringArray, filled *[]string) {
	var added bool
	*dst, added = unionTags(*dst, sourceValues(sources, tags)...)
	if added {
		*filled = append(*filled, "tags")
	}
}

// unionTags appends the sources' tags to the target's; reports whether any
// were added.
func unionTags(target pq.StringArray, sources ...pq.StringArray) (pq.StringArray, bool) {
	before := normalizeTags(target)
	all := append([]string{}, before...)
	for _, s := range sources {
		all = append(all, s...)
	}
	out := normalizeTags(all)
	return pq.StringArray(out), len(out) != len(before)
}

// fillCompanyFields folds sources into target. website (unique by derived
// domain), tax_id and branch_code are conflict-checked; tax_id is taken
// together with its branch_code, as the pair identifies a legal entity.
func fillCompanyFields(target *models.Company, sources []*models.Company) (filled []string, conflicts []mergeConflict) {
	filled = []string{}
	conflicts = []mergeConflict{}
	pick := func(get func(*models.Company) string) []string { return sourceValues(sources, get) }
	pickPtr := func(get func(*models.Company) *string) []*string { return sourceValues(sources, get) }

	fillString(&target.Industry, "industry", &filled, pick(func(c *models.Company) string { return c.Industry }))
	fillString(&target.Size, "size", &filled, pick(func(c *models.Company) string { return c.Size }))
	fillString(&target.RevenueSize, "revenue_size", &filled, pick(func(c *models.Company) string { return c.RevenueSize }))
	fillString(&target.Notes, "notes", &filled, pick(func(c *models.Company) string { return c.Notes }))
	fillPtr(&target.LegalName, "legal_name", &filled, pickPtr(func(c *models.Company) *string { return c.LegalName }))
	fillPtr(&target.Address, "address", &filled, pickPtr(func(c *models.Company) *string { return c.Address }))
	fillPtr(&target.PostalCode, "postal_code", &filled, pickPtr(func(c *models.Company) *string { return c.PostalCode }))

	// website: compared by derived domain (what the unique index is on),
	// falling back to the case-insensitive URL when there's no host.
	websiteKey := func(w string) string {
		if d := utils.ExtractDomain(w); d != "" {
			return d
		}
		return strings.ToLower(strings.TrimSpace(w))
	}
	if blank(target.Website) {
		for _, s := range sources {
			if !blank(s.Website) {
				target.Website = s.Website
				target.Domain = utils.ExtractDomain(s.Website)
				filled = append(filled, "website")
				break
			}
		}
	}

	// tax_id + branch_code.
	if blankPtr(target.TaxID) {
		for _, s := range sources {
			if !blankPtr(s.TaxID) {
				tax := *s.TaxID
				target.TaxID = &tax
				filled = append(filled, "tax_id")
				if !blankPtr(s.BranchCode) {
					branch := *s.BranchCode
					target.BranchCode = &branch
					filled = append(filled, "branch_code")
				}
				break
			}
		}
	} else if blankPtr(target.BranchCode) {
		for _, s := range sources {
			if !blankPtr(s.TaxID) && *s.TaxID == *target.TaxID && !blankPtr(s.BranchCode) {
				branch := *s.BranchCode
				target.BranchCode = &branch
				filled = append(filled, "branch_code")
				break
			}
		}
	}

	for _, s := range sources {
		if !blank(s.Website) && websiteKey(s.Website) != websiteKey(target.Website) {
			conflicts = append(conflicts, mergeConflict{Field: "website", SourceID: s.ID, Value: s.Website})
		}
		if blankPtr(s.TaxID) {
			continue
		}
		if *s.TaxID != *target.TaxID {
			conflicts = append(conflicts, mergeConflict{Field: "tax_id", SourceID: s.ID, Value: *s.TaxID})
		} else if !blankPtr(s.BranchCode) && !blankPtr(target.BranchCode) && *s.BranchCode != *target.BranchCode {
			conflicts = append(conflicts, mergeConflict{Field: "branch_code", SourceID: s.ID, Value: *s.BranchCode})
		}
	}

	mergeSourceTags(&target.Tags, sources, func(c *models.Company) pq.StringArray { return c.Tags }, &filled)
	return filled, conflicts
}

// fillContactFields folds sources into target. email and phone identify a
// Contact (duplicate detection matches on them), so they're conflict-checked
// in their normalized forms. company_id never changes. is_primary is taken
// only from a source in the target's own Company.
func fillContactFields(target *models.Contact, sources []*models.Contact) (filled []string, conflicts []mergeConflict) {
	filled = []string{}
	conflicts = []mergeConflict{}
	pick := func(get func(*models.Contact) string) []string { return sourceValues(sources, get) }
	fillString(&target.Email, "email", &filled, pick(func(c *models.Contact) string { return c.Email }))
	fillString(&target.Phone, "phone", &filled, pick(func(c *models.Contact) string { return c.Phone }))
	fillString(&target.RoleTitle, "role_title", &filled, pick(func(c *models.Contact) string { return c.RoleTitle }))

	if !target.IsPrimary {
		for _, s := range sources {
			if s.IsPrimary && s.CompanyID == target.CompanyID {
				target.IsPrimary = true
				filled = append(filled, "is_primary")
				break
			}
		}
	}

	for _, s := range sources {
		if !blank(s.Email) && utils.NormalizeEmail(s.Email) != utils.NormalizeEmail(target.Email) {
			conflicts = append(conflicts, mergeConflict{Field: "email", SourceID: s.ID, Value: s.Email})
		}
		if !blank(s.Phone) && utils.NormalizePhone(s.Phone) != utils.NormalizePhone(target.Phone) {
			conflicts = append(conflicts, mergeConflict{Field: "phone", SourceID: s.ID, Value: s.Phone})
		}
	}

	mergeSourceTags(&target.Tags, sources, func(c *models.Contact) pq.StringArray { return c.Tags }, &filled)
	return filled, conflicts
}
