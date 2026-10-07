// Package users holds the use cases that manage users.
package users

// Service runs the user management use cases: the first user and the forced password of
// the operator; the list, the invitation, its reissue and its revocation, and the password
// reset link of the users page, which any signed-in user may run; and the acceptance of an
// invitation and the completion of a reset by the holder of the link.
type Service struct {
	users    Users
	links    Links
	sessions sessionRepository
	opener   sessionOpener
	hasher   passwordHasher
	tokens   tokenIssuer
	clock    clock
	tx       txManager
	origin   PublicOrigin
}

// NewService returns a Service on its collaborators that builds links from origin.
func NewService(
	users Users, links Links, sessions sessionRepository, opener sessionOpener,
	hasher passwordHasher, tokens tokenIssuer, clock clock, tx txManager, origin PublicOrigin,
) *Service {
	return &Service{
		users: users, links: links, sessions: sessions, opener: opener, hasher: hasher,
		tokens: tokens, clock: clock, tx: tx, origin: origin,
	}
}
