package utils

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/igeargeek/sales-system-api/internal/models"
)

func TestDealValueFromQuote(t *testing.T) {
	priced := &models.Quote{
		Items:         models.JSONItems{{Description: "x", Qty: 3, Price: 333.333}},
		DiscountTotal: 100, PriceType: models.QuotePriceTypeExclTax, VatEnabled: true,
	}
	value, ok := DealValueFromQuote(priced)
	assert.True(t, ok)
	assert.Equal(t, 900.0, value, "taxable amount (1000.00 - 100), pre-VAT, rounded to satang")

	_, ok = DealValueFromQuote(&models.Quote{Items: models.JSONItems{}})
	assert.False(t, ok, "no priced items syncs nothing")
}

func TestSameMoney(t *testing.T) {
	assert.True(t, SameMoney(100, 100.004))
	assert.False(t, SameMoney(100, 100.01))
}
