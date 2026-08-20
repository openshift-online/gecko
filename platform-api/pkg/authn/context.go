package authn

import "context"

type contextKey struct{}

// WithUser returns a new context carrying the authenticated user email.
func WithUser(ctx context.Context, email string) context.Context {
	return context.WithValue(ctx, contextKey{}, email)
}

// UserFromContext extracts the authenticated user email from the context.
// Returns the email and true if present, or empty string and false otherwise.
func UserFromContext(ctx context.Context) (string, bool) {
	email, ok := ctx.Value(contextKey{}).(string)
	return email, ok
}
