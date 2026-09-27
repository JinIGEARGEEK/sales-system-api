package utils

import (
	"reflect"

	"gorm.io/gorm"
)

// CreateKeepingFalse inserts value like db.Create, except that a bool field
// tagged `default:true` that is explicitly false stays false.
//
// GORM leaves a zero-value field with a default out of the INSERT and reads
// the column default back into the struct, even with Select("*") — so a
// NOT NULL DEFAULT true flag (Quote.VatEnabled, NotificationRule.IsActive/
// CreateTask) can't be created false. This writes those columns back as
// false in the same transaction as the insert, so a failure leaves no row
// behind with the wrong flags. Only bools: every other defaulted column in
// this schema wants its default when left empty.
func CreateKeepingFalse(db *gorm.DB, value interface{}) error {
	stmt := &gorm.Statement{DB: db}
	if err := stmt.Parse(value); err != nil {
		return err
	}
	rv := reflect.Indirect(reflect.ValueOf(value))
	falseCols := map[string]interface{}{}
	for _, f := range stmt.Schema.Fields {
		if f.DBName == "" || f.FieldType.Kind() != reflect.Bool || f.DefaultValueInterface != true {
			continue
		}
		if v, _ := f.ValueOf(db.Statement.Context, rv); v == false {
			falseCols[f.DBName] = false
		}
	}
	return db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(value).Error; err != nil {
			return err
		}
		if len(falseCols) == 0 {
			return nil
		}
		if err := tx.Model(value).UpdateColumns(falseCols).Error; err != nil {
			return err
		}
		for _, f := range stmt.Schema.Fields {
			if _, ok := falseCols[f.DBName]; ok {
				if err := f.Set(db.Statement.Context, rv, false); err != nil {
					return err
				}
			}
		}
		return nil
	})
}
