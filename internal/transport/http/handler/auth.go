// Package handler contains HTTP handlers for the REST API.
package handler

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/izzam/mini-exchange/internal/infra/auth"
	"github.com/izzam/mini-exchange/pkg/response"
)

// decodeJSON decodes the JSON body of r into dst.
func decodeJSON(r *http.Request, dst interface{}) error {
	return json.NewDecoder(r.Body).Decode(dst)
}

// AuthHandler handles user registration and login.
type AuthHandler struct {
	svc *auth.Service
}

// NewAuthHandler creates an AuthHandler backed by the given auth Service.
func NewAuthHandler(svc *auth.Service) *AuthHandler {
	return &AuthHandler{svc: svc}
}

// registerRequest is the expected body for POST /api/v1/auth/register.
type registerRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// loginRequest is the expected body for POST /api/v1/auth/login.
type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// Register handles POST /api/v1/auth/register.
func (h *AuthHandler) Register(w http.ResponseWriter, r *http.Request) {
	var req registerRequest
	if err := decodeJSON(r, &req); err != nil {
		response.Error(w, http.StatusBadRequest, "INVALID_BODY", err.Error())
		return
	}
	if req.Username == "" || req.Password == "" {
		response.Error(w, http.StatusBadRequest, "VALIDATION_ERROR", "username and password are required")
		return
	}

	u, err := h.svc.Register(r.Context(), req.Username, req.Password)
	if err != nil {
		if errors.Is(err, auth.ErrUserExists) {
			response.Error(w, http.StatusConflict, "USER_EXISTS", "username already taken")
			return
		}
		response.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "registration failed")
		return
	}

	token, expiresAt, err := h.svc.Login(req.Username, req.Password)
	if err != nil {
		response.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "auto-login after register failed")
		return
	}

	response.JSON(w, http.StatusCreated, map[string]interface{}{
		"user":       u,
		"token":      token,
		"expires_at": expiresAt,
	})
}

// Login handles POST /api/v1/auth/login.
func (h *AuthHandler) Login(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := decodeJSON(r, &req); err != nil {
		response.Error(w, http.StatusBadRequest, "INVALID_BODY", err.Error())
		return
	}

	token, expiresAt, err := h.svc.Login(req.Username, req.Password)
	if err != nil {
		if errors.Is(err, auth.ErrInvalidCredentials) {
			response.Error(w, http.StatusUnauthorized, "INVALID_CREDENTIALS", "username or password is incorrect")
			return
		}
		response.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "login failed")
		return
	}

	response.JSON(w, http.StatusOK, map[string]interface{}{
		"token":      token,
		"expires_at": expiresAt,
	})
}
