package apierr

import "unicode"

// Fields collects the field errors of one request, so a service reports all of them at once.
type Fields []FieldError

// Add appends one field error. message never holds the rejected value.
func (f *Fields) Add(field, message string) {
	*f = append(*f, FieldError{Field: field, Message: message})
}

// NoControl adds a field error when s has a control character: any rune for which
// unicode.IsControl is true, and the line and paragraph separators U+2028 and U+2029.
func (f *Fields) NoControl(field, s string) {
	for _, r := range s {
		if unicode.IsControl(r) || r == ' ' || r == ' ' {
			f.Add(field, "must not have control characters.")
			return
		}
	}
}

// Err returns nil when f is empty, else a 400 error that lists every item.
func (f Fields) Err() error {
	if len(f) == 0 {
		return nil
	}
	return InvalidFields(f...)
}
