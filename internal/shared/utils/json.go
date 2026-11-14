package utils

import (
	jsonv1 "encoding/json"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
)

var preserveJSONNumbers = json.WithUnmarshalers(
	json.UnmarshalFromFunc(func(decoder *jsontext.Decoder, value *any) error {
		if decoder.PeekKind() != '0' {
			return errors.ErrUnsupported
		}
		raw, err := decoder.ReadValue()
		if err != nil {
			return err
		}
		*value = jsonv1.Number(raw)
		return nil
	}),
)

// PreserveJSONNumbers decodes numbers in interface values without rounding them to float64.
func PreserveJSONNumbers() json.Options {
	return preserveJSONNumbers
}
