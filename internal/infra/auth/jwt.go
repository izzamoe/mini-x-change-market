// Package auth provides JWT-based authentication for the mini-exchange.
package auth

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/izzam/mini-exchange/internal/domain/entity"
	"github.com/izzam/mini-exchange/internal/domain/repository"
	"golang.org/x/crypto/bcrypt"
)

// ErrUserNotFound is returned when the requested user does not exist.
var ErrUserNotFound = errors.New("user not found")

// ErrUserExists is returned when a username is already taken.
var ErrUserExists = errors.New("username already exists")

// ErrInvalidCredentials is returned when password verification fails.
var ErrInvalidCredentials = errors.New("invalid credentials")

// ErrInvalidToken is returned when a JWT token cannot be validated.
var ErrInvalidToken = errors.New("invalid or expired token")

// Claims are the JWT payload fields.
type Claims struct {
	UserID   string `json:"user_id"`
	Username string `json:"username"`
	jwt.RegisteredClaims
}

// Service provides user registration, login, and token validation.
type Service struct {
	secret []byte
	expiry time.Duration

	mu    sync.RWMutex
	users map[string]*entity.User // keyed by username
	byID  map[string]*entity.User // keyed by user ID

	// userRepo is optional. When set, users are persisted to PostgreSQL.
	userRepo repository.UserRepository
}

// NewService creates an AuthService.
func NewService(secret string, expiry time.Duration) *Service {
	return &Service{
		secret: []byte(secret),
		expiry: expiry,
		users:  make(map[string]*entity.User),
		byID:   make(map[string]*entity.User),
	}
}

// SetUserRepo attaches a persistent UserRepository to the service.
// Call LoadUsers after this to warm the in-memory cache from the database.
func (s *Service) SetUserRepo(repo repository.UserRepository) {
	s.userRepo = repo
}

// LoadUsers loads all users from the attached UserRepository into the
// in-memory cache. Call once at startup after SetUserRepo.
func (s *Service) LoadUsers(ctx context.Context) error {
	if s.userRepo == nil {
		return nil
	}
	users, err := s.userRepo.FindAll(ctx)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, u := range users {
		s.users[u.Username] = u
		s.byID[u.ID] = u
	}
	slog.Info("auth: loaded users from database", "count", len(users))
	return nil
}

// Register creates a new user with a bcrypt-hashed password.
// Returns ErrUserExists if the username is taken.
func (s *Service) Register(username, password string) (*entity.User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.users[username]; exists {
		return nil, ErrUserExists
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}

	u := &entity.User{
		ID:           uuid.NewString(),
		Username:     username,
		PasswordHash: string(hash),
		CreatedAt:    time.Now(),
	}

	// Persist to PostgreSQL when a repo is wired.
	if s.userRepo != nil {
		if err := s.userRepo.Save(context.Background(), u); err != nil {
			if errors.Is(err, repository.ErrDuplicate) {
				return nil, ErrUserExists
			}
			return nil, err
		}
	}

	s.users[username] = u
	s.byID[u.ID] = u
	return u, nil
}

// Login verifies credentials and returns a signed JWT on success.
func (s *Service) Login(username, password string) (string, time.Time, error) {
	s.mu.RLock()
	u, exists := s.users[username]
	s.mu.RUnlock()

	if !exists {
		return "", time.Time{}, ErrInvalidCredentials
	}

	if err := bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)); err != nil {
		return "", time.Time{}, ErrInvalidCredentials
	}

	expiresAt := time.Now().Add(s.expiry)
	claims := Claims{
		UserID:   u.ID,
		Username: u.Username,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   u.ID,
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			ExpiresAt: jwt.NewNumericDate(expiresAt),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString(s.secret)
	if err != nil {
		return "", time.Time{}, err
	}
	return signed, expiresAt, nil
}

// ValidateToken parses and validates a JWT string.
// Returns Claims on success, or ErrInvalidToken on failure.
func (s *Service) ValidateToken(tokenString string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &Claims{}, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, ErrInvalidToken
		}
		return s.secret, nil
	})
	if err != nil || !token.Valid {
		return nil, ErrInvalidToken
	}

	claims, ok := token.Claims.(*Claims)
	if !ok {
		return nil, ErrInvalidToken
	}
	return claims, nil
}

// GetUserByID returns a user by ID or ErrUserNotFound.
func (s *Service) GetUserByID(id string) (*entity.User, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	u, ok := s.byID[id]
	if !ok {
		return nil, ErrUserNotFound
	}
	return u, nil
}
