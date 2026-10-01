package models

type NotificationEntityType string

const (
	NotificationEntityDeal     NotificationEntityType = "deal"
	NotificationEntityQuote    NotificationEntityType = "quote"
	NotificationEntityContract NotificationEntityType = "contract"
	// NotificationEntityProspect — added 2026-09-03, Marketing's own
	// staleness rule (FR-CRM-107). See NotificationRule's doc comment below.
	NotificationEntityProspect NotificationEntityType = "prospect"
	// NotificationEntityCompany — added for the dormant-customer / upsell
	// -targeting feature. See NotificationRule's doc comment below.
	NotificationEntityCompany NotificationEntityType = "company"
	// NotificationEntityPaymentInstallment — added alongside PaymentInstallment
	// itself (the payment schedule feature). See NotificationRule's doc
	// comment below.
	NotificationEntityPaymentInstallment NotificationEntityType = "payment_installment"
	// NotificationEntityCustomerProductRenewal / NotificationEntityContractExpiry
	// — the renewal reminders for packaged Products (charged via Contracts).
	// See NotificationRule's doc comment below.
	NotificationEntityCustomerProductRenewal NotificationEntityType = "customer_product_renewal"
	NotificationEntityContractExpiry         NotificationEntityType = "contract_expiry"
)

var ValidNotificationEntityTypes = []NotificationEntityType{
	NotificationEntityDeal, NotificationEntityQuote, NotificationEntityContract, NotificationEntityProspect,
	NotificationEntityCompany, NotificationEntityPaymentInstallment,
	NotificationEntityCustomerProductRenewal, NotificationEntityContractExpiry,
}

func IsValidNotificationEntityType(v NotificationEntityType) bool {
	for _, t := range ValidNotificationEntityTypes {
		if t == v {
			return true
		}
	}
	return false
}

type NotificationRecipientRole string

const (
	NotificationRecipientOwner            NotificationRecipientRole = "owner"
	NotificationRecipientOwnerAndManagers NotificationRecipientRole = "owner_and_managers"
)

var ValidNotificationRecipientRoles = []NotificationRecipientRole{
	NotificationRecipientOwner, NotificationRecipientOwnerAndManagers,
}

func IsValidNotificationRecipientRole(v NotificationRecipientRole) bool {
	for _, r := range ValidNotificationRecipientRoles {
		if r == v {
			return true
		}
	}
	return false
}

// NotificationRule is an Admin-configurable workflow automation rule
// (FR-CRM-100/101/102). EntityType picks which one, fixed condition applies —
// deliberately one rule shape per entity type (not a free-form condition
// expression) so a new threshold/recipient is pure config, no new backend
// hook code (FR-CRM-102), while avoiding a rule DSL this app has no other
// need for:
//
//   - "deal": an open Deal (not yet Won/Lost) sitting in its current stage
//     for at least ThresholdDays since its last stage_changed audit entry
//     (or Deal.CreatedAt if it never changed stage) — FR-CRM-100.
//   - "quote": a Sent Quote whose validity_date falls within ThresholdDays
//     from now — same definition as the Quotes Expiring Soon report
//     (FR-CRM-096) — FR-CRM-101.
//   - "contract": a Draft/Sent Contract that has sat unsigned for at least
//     ThresholdDays since creation — same definition as the Contracts Stuck
//     report (FR-CRM-097) — FR-CRM-101.
//   - "prospect": a Prospect not yet Converted/Disqualified (i.e. still
//     actively being worked) that has gone at least ThresholdDays since
//     UpdatedAt with no change — FR-CRM-107, Marketing's own funnel, added
//     2026-09-03. Unlike "deal", there's no audit-log stage-history lookup
//     (Prospect.Status changes aren't separately audited the way Deal.Stage
//     is), so UpdatedAt is the closest available "last touched" signal.
//   - "company": an active Company with no Activity logged directly against
//     it in at least ThresholdDays.
//   - "payment_installment": a PaymentInstallment not yet fully paid (per
//     utils.ComputeInstallmentStatusesFromPayments' waterfall allocation) whose due date
//     is within ThresholdDays from now (covers both "coming due soon" and
//     "already overdue" in one condition, same single-direction-per-type
//     shape every other rule above uses) — added alongside the payment
//     schedule feature.
//   - "customer_product_renewal": an Active CustomerProduct whose
//     renewal_date is at most ThresholdDays ahead and at most
//     RenewalGraceDays behind today (so a renewal entered late, or missed
//     while the server was down, still gets one alert, but a long-stale date
//     doesn't). Fires once per renewal_date value — the log context is the
//     date — so rolling renewal_date forward a year re-arms it.
//   - "contract_expiry": a Signed Contract whose end_date falls in the same
//     window, once per end_date value.
//
// CreateTask (default true) also creates a Task for the entity's owner on
// every firing — the in-app alert, and the only one when SMTP is off.
type NotificationRule struct {
	AuditedModel
	Name string `gorm:"not null;uniqueIndex" json:"name"`
	// varchar(32), not (16) — "payment_installment" (20 chars) needs the
	// extra room; widened rather than shortening the value once RecipientRole
	// on this same struct already uses varchar(32).
	EntityType    NotificationEntityType    `gorm:"type:varchar(32);not null;index" json:"entity_type"`
	ThresholdDays int                       `gorm:"not null" json:"threshold_days"`
	RecipientRole NotificationRecipientRole `gorm:"type:varchar(32);not null;default:'owner'" json:"recipient_role"`
	IsActive      bool                      `gorm:"not null;default:true;index" json:"is_active"`
	// CreateTask — AutoMigrate adds this NOT NULL DEFAULT true, which also
	// backfills every pre-existing rule to true. Create must write an
	// explicit false itself (GORM omits zero values for defaulted columns).
	CreateTask bool `gorm:"not null;default:true" json:"create_task"`
}

// RenewalGraceDays is how far past a renewal_date/end_date the
// customer_product_renewal and contract_expiry rules still fire.
const RenewalGraceDays = 30

// DefaultNotificationRules are seeded (cmd/api/main.go) when no rule of
// their entity type exists yet. Only the renewal pair: every other type
// predates seeding and existing deployments already configured theirs.
// Active by default — they only match records with renewal_date/end_date
// set, which nothing has until someone enters one.
var DefaultNotificationRules = []NotificationRule{
	{Name: "Product renewal coming up", EntityType: NotificationEntityCustomerProductRenewal, ThresholdDays: 30,
		RecipientRole: NotificationRecipientOwner, IsActive: true, CreateTask: true},
	{Name: "Contract ending soon", EntityType: NotificationEntityContractExpiry, ThresholdDays: 30,
		RecipientRole: NotificationRecipientOwner, IsActive: true, CreateTask: true},
}

func (NotificationRule) TableName() string { return "notification_rules" }
