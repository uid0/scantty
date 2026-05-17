package omsapi

import (
	"bytes"
	"strconv"
)

// DecimalString holds a monetary or decimal value from the OMS API as a
// string ("12.34"). The API is inconsistent: fields declared as
// ``serializers.DecimalField`` come over as JSON strings, but fields backed
// by a Python ``@property`` returning a ``Decimal`` come over as JSON
// numbers (or ``null``). This type accepts both forms so the same Go field
// works against either serializer shape.
type DecimalString string

func (d *DecimalString) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) == 0 || bytes.Equal(data, []byte("null")) {
		*d = ""
		return nil
	}
	// Already a JSON string — unwrap quotes via strconv.Unquote so
	// escape sequences are handled the same way encoding/json would.
	if data[0] == '"' {
		s, err := strconv.Unquote(string(data))
		if err != nil {
			return err
		}
		*d = DecimalString(s)
		return nil
	}
	// JSON number — store the literal verbatim so callers control rounding.
	*d = DecimalString(data)
	return nil
}

// String exposes the value for fmt.Stringer-aware printing.
func (d DecimalString) String() string { return string(d) }

// Empty reports whether the field is null or unset.
func (d DecimalString) Empty() bool { return d == "" }
