package app

import (
	"context"
	"errors"
	"fmt"
	"github.com/mapherez/nox-backend/backend/internal/storage"
	noxmcp "github.com/mapherez/nox-mcp/go"
	"net/http"
	"strings"
)

// Backend-owned operations: presentation and future JSON adapters share policy.
func (s *Server) webUser(r *http.Request) (storage.User, bool, error) {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil {
		return storage.User{}, false, nil
	}
	return s.store.UserBySessionToken(r.Context(), cookie.Value)
}
func (s *Server) managedUser(ctx context.Context, actor storage.User, id string) (storage.User, error) {
	if actor.Role != storage.UserRoleAdmin || actor.Status != storage.UserStatusActive {
		return storage.User{}, storage.ErrForbidden
	}
	return s.store.UserByID(ctx, id)
}
func (s *Server) addManagedUser(ctx context.Context, actor storage.User, email, role string) (storage.User, error) {
	if actor.Role != storage.UserRoleAdmin || actor.Status != storage.UserStatusActive {
		return storage.User{}, storage.ErrForbidden
	}
	role = strings.ToUpper(strings.TrimSpace(role))
	if role == "" {
		role = storage.UserRoleUser
	}
	if role != storage.UserRoleUser && role != storage.UserRoleAdmin {
		return storage.User{}, fmt.Errorf("%w: role must be USER or ADMIN", storage.ErrBadRequest)
	}
	existing, err := s.store.UserByEmail(ctx, email)
	if err != nil && !errors.Is(err, storage.ErrNotFound) {
		return storage.User{}, err
	}
	if err == nil && existing.Role == storage.UserRoleAdmin && role != storage.UserRoleAdmin {
		return storage.User{}, fmt.Errorf("%w: admin users cannot be demoted", storage.ErrBadRequest)
	}
	return s.store.UpsertManagedUser(ctx, email, role)
}
func (s *Server) setManagedUserStatus(ctx context.Context, actor storage.User, id, status string) error {
	status = strings.ToUpper(strings.TrimSpace(status))
	target, err := s.managedUser(ctx, actor, id)
	if err != nil {
		return err
	}
	if target.ID == actor.ID && status == storage.UserStatusDisabled {
		return fmt.Errorf("%w: admins cannot disable their own account", storage.ErrBadRequest)
	}
	if target.Role == storage.UserRoleAdmin && status == storage.UserStatusDisabled {
		return fmt.Errorf("%w: admin users cannot be disabled", storage.ErrBadRequest)
	}
	return s.store.UpdateManagedUser(ctx, id, "status", status)
}
func (s *Server) setManagedUserRole(ctx context.Context, actor storage.User, id, role string) error {
	role = strings.ToUpper(strings.TrimSpace(role))
	if role != storage.UserRoleUser && role != storage.UserRoleAdmin {
		return fmt.Errorf("%w: role must be USER or ADMIN", storage.ErrBadRequest)
	}
	target, err := s.managedUser(ctx, actor, id)
	if err != nil {
		return err
	}
	if target.ID == actor.ID && role != storage.UserRoleAdmin {
		return fmt.Errorf("%w: admins cannot demote their own account", storage.ErrBadRequest)
	}
	if target.Role == storage.UserRoleAdmin && role != storage.UserRoleAdmin {
		return fmt.Errorf("%w: admin users cannot be demoted", storage.ErrBadRequest)
	}
	return s.store.UpdateManagedUser(ctx, id, "role", role)
}
func (s *Server) deleteManagedUser(ctx context.Context, actor storage.User, id string) error {
	target, err := s.managedUser(ctx, actor, id)
	if err != nil {
		return err
	}
	if target.ID == actor.ID || target.Role == storage.UserRoleAdmin {
		return fmt.Errorf("%w: admin users cannot be deleted", storage.ErrBadRequest)
	}
	return s.store.DeleteManagedUser(ctx, id)
}

func (s *Server) saveFoundationConnection(ctx context.Context, actor storage.User, service, origin, revision string) (storage.FoundationConnection, error) {
	if actor.Role != storage.UserRoleAdmin || actor.Status != storage.UserStatusActive {
		return storage.FoundationConnection{}, storage.ErrForbidden
	}
	origin, err := noxmcp.NormalizeSuiteOrigin(strings.TrimSpace(origin))
	if err != nil {
		return storage.FoundationConnection{}, fmt.Errorf("%w: origin must be an HTTPS IP/hostname with optional port, without a base path, credentials, query or fragment", storage.ErrBadRequest)
	}
	return s.store.SaveFoundationConnection(ctx, service, origin, revision)
}
