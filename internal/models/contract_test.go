package models

import (
	"testing"
	"time"
)

func TestContract_EffectiveStatusAt(t *testing.T) {
	endDate := time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC) // a date column
	// 30 Sep 23:30 Bangkok is still the last day; 1 Oct 00:30 Bangkok (still
	// 30 Sep in UTC) is past it.
	lastDay := time.Date(2026, 9, 30, 23, 30, 0, 0, time.Local)
	dayAfter := time.Date(2026, 10, 1, 0, 30, 0, 0, time.Local)

	cases := []struct {
		name   string
		c      Contract
		now    time.Time
		expect ContractStatus
	}{
		{"signed, last day in force", Contract{Status: ContractStatusSigned, EndDate: &endDate}, lastDay, ContractStatusSigned},
		{"signed, past end date", Contract{Status: ContractStatusSigned, EndDate: &endDate}, dayAfter, ContractStatusExpired},
		{"signed, no end date", Contract{Status: ContractStatusSigned}, dayAfter, ContractStatusSigned},
		{"draft past end date stays draft", Contract{Status: ContractStatusDraft, EndDate: &endDate}, dayAfter, ContractStatusDraft},
		{"sent past end date stays sent", Contract{Status: ContractStatusSent, EndDate: &endDate}, dayAfter, ContractStatusSent},
		{"stored expired stays expired", Contract{Status: ContractStatusExpired, EndDate: &endDate}, lastDay, ContractStatusExpired},
	}
	for _, tc := range cases {
		if got := tc.c.EffectiveStatusAt(tc.now); got != tc.expect {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.expect)
		}
		if tc.c.Status != tc.c.EffectiveStatusAt(tc.now) && tc.c.Status != ContractStatusSigned {
			t.Errorf("%s: only a signed contract may derive a different status", tc.name)
		}
	}
}

func TestDeal_SyncWonAt(t *testing.T) {
	first := time.Date(2026, 9, 1, 9, 0, 0, 0, time.Local)
	later := first.Add(48 * time.Hour)

	d := Deal{Status: DealStatusOpen}
	d.SyncWonAt(first)
	if d.WonAt != nil {
		t.Fatal("open deal must have no won_at")
	}
	d.Status = DealStatusWon
	d.SyncWonAt(first)
	if d.WonAt == nil || !d.WonAt.Equal(first) {
		t.Fatalf("entering won must stamp now, got %v", d.WonAt)
	}
	d.SyncWonAt(later)
	if !d.WonAt.Equal(first) {
		t.Fatal("re-saving a won deal must keep its original stamp")
	}
	d.Status = DealStatusLost
	d.SyncWonAt(later)
	if d.WonAt != nil {
		t.Fatal("leaving won must clear won_at")
	}
}
