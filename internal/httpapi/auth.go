package httpapi

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type registerRequest struct {
	Name     string `json:"name"`
	Password string `json:"password"`
}

type loginRequest struct {
	Name     string `json:"name"`
	Password string `json:"password"`
}

type authUser struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Role string `json:"role"`
}

type loginResponse struct {
	Token     string   `json:"token"`
	ExpiresAt int64    `json:"expiresAt"`
	User      authUser `json:"user"`
}

type pubkeyRequest struct {
	Pubkey string `json:"pubkey"`
}

func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	var req registerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" || req.Password == "" {
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", "name and password required", false)
		return
	}
	u, err := s.core.RegisterUser(req.Name, req.Password)
	if err != nil {
		writeError(w, http.StatusConflict, "conflict", err.Error(), false)
		return
	}
	tok, exp, err := s.mintToken(u.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "could not issue token", true)
		return
	}
	writeJSON(w, http.StatusCreated, loginResponse{
		Token: tok, ExpiresAt: exp,
		User: authUser{ID: u.ID, Name: u.Name, Role: u.Role},
	})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" || req.Password == "" {
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", "name and password required", false)
		return
	}
	u, err := s.core.AuthenticateUser(req.Name, req.Password)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "unauthenticated", "invalid credentials", false)
		return
	}
	tok, exp, err := s.mintToken(u.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "could not issue token", true)
		return
	}
	writeJSON(w, http.StatusOK, loginResponse{
		Token: tok, ExpiresAt: exp,
		User: authUser{ID: u.ID, Name: u.Name, Role: u.Role},
	})
}

func (s *Server) handleAddPubkey(w http.ResponseWriter, r *http.Request) {
	actor := userFromCtx(r.Context())
	var req pubkeyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Pubkey == "" {
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", "pubkey required", false)
		return
	}
	if err := s.core.AddPubkey(actor.ID, req.Pubkey); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error(), true)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) mintToken(userID string) (string, int64, error) {
	exp := time.Now().Add(30 * 24 * time.Hour)
	claims := jwt.MapClaims{
		"sub": userID,
		"exp": exp.Unix(),
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := tok.SignedString(s.jwtSecret)
	return signed, exp.UnixMilli(), err
}
