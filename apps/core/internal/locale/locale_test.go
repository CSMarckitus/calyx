package locale_test

import (
	"strings"
	"testing"

	"calyx/core/internal/locale"
)

func TestParseAcceptsLanguageAndCountry(t *testing.T) {
	cases := map[string]locale.Locale{
		"es-ES": {Language: "es", Country: "ES"},
		"pt-BR": {Language: "pt", Country: "BR"},
	}
	for tag, want := range cases {
		got, err := locale.Parse(tag)
		if err != nil {
			t.Fatalf("%s: %v", tag, err)
		}
		if got != want || got.String() != tag {
			t.Errorf("%s: got %+v (%s)", tag, got, got)
		}
	}
}

func TestParseRefusesOtherForms(t *testing.T) {
	for _, tag := range []string{"es", "es-419", "zh-Hant-TW", "es_ES", "ES-es", "es-es", ""} {
		_, err := locale.Parse(tag)
		if err == nil {
			t.Errorf("%q: accepted", tag)
			continue
		}
		if !strings.Contains(err.Error(), "must be a locale like es-ES: two lower-case letters, a hyphen, two upper-case letters") {
			t.Errorf("%q: error %q", tag, err)
		}
	}
}
