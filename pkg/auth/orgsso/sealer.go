package orgsso

import (
	"errors"
	"fmt"

	"github.com/arketov/iterion/pkg/secrets"
)

// SealClientSecret seals an OIDC provider's client_secret, binding the sealed
// blob to the provider id via AAD "org_sso_provider:<id>" (same convention as
// forge_oauth_app:<id> / generic_secret:<id>) so a sealed payload can't be
// silently transplanted onto another provider record or tenant.
func SealClientSecret(sealer secrets.Sealer, providerID, clientSecret string) ([]byte, error) {
	if sealer == nil {
		return nil, errors.New("orgsso: nil sealer")
	}
	return sealer.Seal([]byte(clientSecret), aad(providerID))
}

// OpenClientSecret returns an OIDC provider's client_secret from its sealed blob.
func OpenClientSecret(sealer secrets.Sealer, providerID string, sealed []byte) (string, error) {
	if sealer == nil {
		return "", errors.New("orgsso: nil sealer")
	}
	raw, err := sealer.Open(sealed, aad(providerID))
	if err != nil {
		return "", fmt.Errorf("orgsso: open client secret: %w", err)
	}
	return string(raw), nil
}

func aad(providerID string) []byte {
	return []byte("org_sso_provider:" + providerID)
}
