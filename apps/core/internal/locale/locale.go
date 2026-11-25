// Package locale reads the locale that a WhatsApp Web instance sends to WhatsApp at each
// connection. It checks only the form, not that the codes exist.
package locale

import (
	"fmt"
	"regexp"
)

var form = regexp.MustCompile(`^[a-z]{2}-[A-Z]{2}$`)

// Locale is a language (ISO 639-1) and a country (ISO 3166-1 alpha-2).
type Locale struct {
	Language, Country string
}

func (l Locale) String() string { return l.Language + "-" + l.Country }

// Parse accepts only the form "es-ES".
func Parse(tag string) (Locale, error) {
	if !form.MatchString(tag) {
		return Locale{}, fmt.Errorf("%q must be a locale like es-ES: two lower-case letters, a hyphen, two upper-case letters", tag)
	}
	return Locale{Language: tag[:2], Country: tag[3:]}, nil
}
