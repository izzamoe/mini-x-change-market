package entity

import "time"

// User represents an authenticated user of the trading system.
type User struct {
	ID           string    `json:"id"`
	Username     string    `json:"username"`
	PasswordHash string    `json:"-"` // never serialised to JSON
	CreatedAt    time.Time `json:"created_at"`
}
