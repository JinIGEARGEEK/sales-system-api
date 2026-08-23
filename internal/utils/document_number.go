package utils

import (
	"fmt"
	"time"

	"gorm.io/gorm"
)

// NextDocumentNumber returns the next race-safe sequential number for a
// document type + month (e.g. "QT2026080004" for docType "QT" at a time in
// August 2026), formatted as "{docType}{YYYYMM}{seq:03d}". Callers must run
// this inside the same transaction (tx) as the row it's stamping onto, so a
// failed insert rolls the sequence increment back too — otherwise a retried
// create after a failed save would burn numbers.
//
// Race-safety: a single INSERT ... ON CONFLICT DO UPDATE ... RETURNING
// statement, not a separate SELECT-then-UPDATE — Postgres row-locks the
// conflicting row for the statement's duration, so two concurrent calls for
// the same prefix serialize on the DB rather than racing in application code.
func NextDocumentNumber(tx *gorm.DB, docType string, at time.Time) (string, error) {
	prefix := fmt.Sprintf("%s%s", docType, at.Format("200601"))

	var seq int
	row := tx.Raw(`
		INSERT INTO document_sequences (prefix, seq)
		VALUES (?, 1)
		ON CONFLICT (prefix) DO UPDATE SET seq = document_sequences.seq + 1
		RETURNING seq
	`, prefix).Row()
	if err := row.Scan(&seq); err != nil {
		return "", fmt.Errorf("allocate document number for prefix %s: %w", prefix, err)
	}

	return fmt.Sprintf("%s%03d", prefix, seq), nil
}
