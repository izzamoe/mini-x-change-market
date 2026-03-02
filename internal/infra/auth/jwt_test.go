package auth

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestService(t *testing.T) *Service {
	t.Helper()
	return NewService("test-secret-key", time.Hour)
}

func TestRegister_Success(t *testing.T) {
	svc := newTestService(t)
	u, err := svc.Register("alice", "password123")
	require.NoError(t, err)
	assert.Equal(t, "alice", u.Username)
	assert.NotEmpty(t, u.ID)
	assert.NotEmpty(t, u.PasswordHash) // stored (bcrypt hash), hidden from JSON via `json:"-"`
}

func TestRegister_DuplicateUsername(t *testing.T) {
	svc := newTestService(t)
	_, err := svc.Register("bob", "pass1")
	require.NoError(t, err)
	_, err = svc.Register("bob", "pass2")
	assert.ErrorIs(t, err, ErrUserExists)
}

func TestLogin_Success(t *testing.T) {
	svc := newTestService(t)
	_, err := svc.Register("carol", "secret")
	require.NoError(t, err)

	token, expiresAt, err := svc.Login("carol", "secret")
	require.NoError(t, err)
	assert.NotEmpty(t, token)
	assert.True(t, expiresAt.After(time.Now()))
}

func TestLogin_WrongPassword(t *testing.T) {
	svc := newTestService(t)
	_, err := svc.Register("dave", "correct")
	require.NoError(t, err)

	_, _, err = svc.Login("dave", "wrong")
	assert.ErrorIs(t, err, ErrInvalidCredentials)
}

func TestLogin_UnknownUser(t *testing.T) {
	svc := newTestService(t)
	_, _, err := svc.Login("nobody", "pass")
	assert.ErrorIs(t, err, ErrInvalidCredentials)
}

func TestValidateToken_Success(t *testing.T) {
	svc := newTestService(t)
	_, err := svc.Register("eve", "pass")
	require.NoError(t, err)

	token, _, err := svc.Login("eve", "pass")
	require.NoError(t, err)

	claims, err := svc.ValidateToken(token)
	require.NoError(t, err)
	assert.Equal(t, "eve", claims.Username)
	assert.NotEmpty(t, claims.UserID)
}

func TestValidateToken_InvalidSignature(t *testing.T) {
	svc := newTestService(t)
	_, err := svc.ValidateToken("not.a.real.token")
	assert.ErrorIs(t, err, ErrInvalidToken)
}

func TestValidateToken_ExpiredToken(t *testing.T) {
	// Service with -1 second expiry so the token is immediately expired.
	svc := NewService("secret", -time.Second)
	_, err := svc.Register("frank", "pass")
	require.NoError(t, err)

	token, _, err := svc.Login("frank", "pass")
	require.NoError(t, err)

	_, err = svc.ValidateToken(token)
	assert.ErrorIs(t, err, ErrInvalidToken)
}

func TestValidateToken_WrongSecret(t *testing.T) {
	svc1 := NewService("secret-A", time.Hour)
	svc2 := NewService("secret-B", time.Hour)

	_, err := svc1.Register("grace", "pass")
	require.NoError(t, err)
	token, _, err := svc1.Login("grace", "pass")
	require.NoError(t, err)

	_, err = svc2.ValidateToken(token)
	assert.ErrorIs(t, err, ErrInvalidToken)
}
