package main

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"calyx/sdktools/internal/spec"
)

// renames maps a schema name that clashes to its class name. Client is a part of the
// namespace Calyx\Client, and Go uses the same name (go/backend/overlay.yaml).
var renames = map[string]string{
	"Client": "OrgClient",
}

// namespaceParts are the parts of the namespace of the model classes. A class with one of
// these names would be confused with the namespace.
var namespaceParts = []string{"calyx", "client", "generated", "model"}

// reservedWords are the PHP keywords and reserved names that cannot be a class name.
var reservedWords = strings.Fields(`
	abstract and array as bool break callable case catch class clone const continue declare
	default do echo else elseif empty enddeclare endfor endforeach endif endswitch endwhile enum
	eval exit extends false final finally float fn for foreach function global goto if
	implements include include_once instanceof insteadof int interface isset iterable list
	match mixed namespace never new null numeric object or print private protected public
	readonly require require_once resource return self static string switch throw trait true
	try unset use var void while xor yield parent`)

// retryCases maps the wire value of the retry advice to the case of the PHP enum RetryAdvice.
var retryCases = map[string]string{
	"retry_now":     "RetryNow",
	"retry_later":   "RetryLater",
	"check_if_sent": "CheckIfSent",
	"do_not_retry":  "DoNotRetry",
	"unknown":       "Unknown",
}

// phpOnlyClientCodes are the client codes of the PHP SDK only (design, "The error"). Their
// constants go in ClientCode; their retry advice is hand-written in RetryAdvice::PHP_ONLY.
var phpOnlyClientCodes = []string{"unsupported_runtime", "pending_send_expired"}

// className returns the PHP class name of a schema. It fails on a clash that is not in the
// rename table.
func className(schema string) (string, error) {
	if name, ok := renames[schema]; ok {
		return name, nil
	}
	lower := strings.ToLower(schema)
	if slices.Contains(reservedWords, lower) || slices.Contains(namespaceParts, lower) {
		return "", fmt.Errorf("the schema name %s clashes with a PHP or SDK name; add it to the rename table of phpgen", schema)
	}
	if !classPattern.MatchString(schema) {
		return "", fmt.Errorf("the schema name %s cannot be a PHP class name", schema)
	}
	return schema, nil
}

var (
	classPattern    = regexp.MustCompile(`^[A-Z][A-Za-z0-9]*$`)
	wirePattern     = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	constantPattern = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)
)

// codeClasses maps a source to the PHP class of its code constants.
var codeClasses = map[string]string{"backend": "BackendCode", "core": "CoreCode", "client": "ClientCode"}

// codeConstant returns the constant name of a code: upper case, without the ERROR_REASON_
// prefix of the core reasons.
func codeConstant(code string) string {
	return strings.TrimPrefix(strings.ToUpper(code), "ERROR_REASON_")
}

// checkAdvice checks that each row has a PHP enum case and a unique PHP constant name.
func checkAdvice(rows []spec.AdviceRow) error {
	seen := map[string]bool{}
	for _, r := range rows {
		if _, ok := retryCases[r.Retry]; !ok {
			return fmt.Errorf("the retry value %q has no case in retryCases", r.Retry)
		}
		name := r.Source + " " + codeConstant(r.Code)
		if !constantPattern.MatchString(codeConstant(r.Code)) || seen[name] {
			return fmt.Errorf("retry-advice.json: the %s code %q gives no unique PHP constant name", r.Source, r.Code)
		}
		seen[name] = true
	}
	for _, code := range phpOnlyClientCodes {
		if seen["client "+codeConstant(code)] {
			return fmt.Errorf("the PHP client code %s is also in retry-advice.json; remove it from phpOnlyClientCodes", code)
		}
	}
	return nil
}
