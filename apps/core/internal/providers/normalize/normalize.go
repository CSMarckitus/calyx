// Package normalize gives the engine the recipient rules of each provider. The engine cannot
// import the providers, so the bootstrap passes Recipient to the orchestrator.
package normalize

import (
	"google.golang.org/grpc/codes"

	"calyx/core/internal/calyxerr"
	"calyx/core/internal/providers/meta"
	"calyx/core/internal/providers/whatsapp"
)

// Recipient gives the same result as Provider.NormalizeRecipient of the named provider.
func Recipient(provider, to string) (string, error) {
	switch provider {
	case "WHATSAPP_WEB":
		return whatsapp.NormalizeRecipient(to)
	case "META_CLOUD":
		return meta.NormalizeRecipient(to)
	}
	return "", calyxerr.New(codes.FailedPrecondition, "This core cannot normalize recipients for %s.", provider)
}
