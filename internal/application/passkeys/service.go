package passkeys

import "time"

// ceremonyTTL is how long a WebAuthn ceremony may take from its begin to its finish.
const ceremonyTTL = 5 * time.Minute

// Service runs the passkey use cases.
type Service struct {
	users      Users
	passkeys   Passkeys
	ceremonies Ceremonies
	webauthn   WebAuthn
	sessions   SessionService
	clock      Clock
}

// NewService returns a Service on its ports.
func NewService(
	users Users, passkeys Passkeys, ceremonies Ceremonies, webauthn WebAuthn, sessions SessionService, clock Clock,
) *Service {
	return &Service{
		users: users, passkeys: passkeys, ceremonies: ceremonies, webauthn: webauthn, sessions: sessions, clock: clock,
	}
}
