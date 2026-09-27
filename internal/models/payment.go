package models

import "time"

type PaymentMethod string

const (
	PaymentMethodCash     PaymentMethod = "cash"
	PaymentMethodTransfer PaymentMethod = "transfer"
	PaymentMethodCard     PaymentMethod = "card"
	PaymentMethodOther    PaymentMethod = "other"
)

// ValidPaymentMethods lists every accepted PaymentMethod value, for
// handler-layer validation — a fixed accounting-category enum, not an
// open-ended list.
var ValidPaymentMethods = []PaymentMethod{
	PaymentMethodCash, PaymentMethodTransfer, PaymentMethodCard, PaymentMethodOther,
}

func IsValidPaymentMethod(m PaymentMethod) bool {
	if m == "" {
		return true
	}
	for _, v := range ValidPaymentMethods {
		if v == m {
			return true
		}
	}
	return false
}

// Payment — api-system-spec.md §7.5. Records money actually received; the
// invoice/receipt itself is issued in FlowAccount, so this only keeps that
// document's number (DocumentNumber) rather than numbering anything here.
type Payment struct {
	HardDeleteModel
	DealID uint `gorm:"not null;index" json:"deal_id"`
	// Amount is the cash that arrived — net of any withholding tax.
	Amount float64       `json:"amount"`
	PaidAt time.Time     `json:"paid_at"`
	Method PaymentMethod `gorm:"type:varchar(16)" json:"method"`
	Note   string        `json:"note"`
	// WhtAmount is the withholding tax the customer deducted and remits to
	// the Revenue Department on our behalf. It settles the invoice just like
	// cash does (see SettledAmount), so a 3% WHT customer paying 97,000 on a
	// 100,000 invoice leaves nothing outstanding.
	WhtAmount float64 `gorm:"not null;default:0" json:"wht_amount"`
	// WhtCertificateReceived tracks whether the customer's withholding tax
	// certificate (หนังสือรับรองการหักภาษี ณ ที่จ่าย, "50 ทวิ") has arrived —
	// accounting needs it to claim the WHT credit, and it often lags the
	// payment by weeks.
	WhtCertificateReceived bool `gorm:"not null;default:false" json:"wht_certificate_received"`
	// DocumentNumber is the FlowAccount receipt / tax-invoice number for this
	// payment, free text, optional.
	DocumentNumber *string `gorm:"type:varchar(64)" json:"document_number"`
	// InstallmentID optionally pins this payment to one PaymentInstallment of
	// the same Deal. Linked payments settle their own installment first; the
	// rest still waterfall in due-date order (utils.
	// ComputeInstallmentStatusesFromPayments). App-level reference only, like
	// every other id column here — PaymentInstallmentHandler.Delete clears it.
	InstallmentID *uint `gorm:"index" json:"installment_id"`
}

func (Payment) TableName() string { return "payments" }

// SettledAmount is how much of the receivable this payment clears: the cash
// received plus the withholding tax the customer deducted from it.
func (p Payment) SettledAmount() float64 { return p.Amount + p.WhtAmount }
