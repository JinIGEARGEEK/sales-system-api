package models

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
)

// JSONItems is []QuoteItem stored as a jsonb column.
type JSONItems []QuoteItem

func (j JSONItems) Value() (driver.Value, error) {
	if j == nil {
		return "[]", nil
	}
	return json.Marshal(j)
}

func (j *JSONItems) Scan(value interface{}) error {
	if value == nil {
		*j = JSONItems{}
		return nil
	}
	bytes, ok := value.([]byte)
	if !ok {
		s, ok := value.(string)
		if !ok {
			return errors.New("JSONItems: unsupported scan type")
		}
		bytes = []byte(s)
	}
	return json.Unmarshal(bytes, j)
}
