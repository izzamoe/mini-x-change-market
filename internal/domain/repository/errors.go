package repository

import "errors"

// ErrNotFound is returned when a requested entity does not exist.
var ErrNotFound = errors.New("entity not found")

// ErrDuplicate is returned when attempting to save an entity with an ID that already exists.
var ErrDuplicate = errors.New("entity already exists")
