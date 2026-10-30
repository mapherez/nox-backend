package app

import (
	"context"
	"fmt"
	"github.com/mapherez/nox-backend/backend/internal/storage"
	"net/http"
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
	if actor.Role != storage.UserRoleAdmin {
		return storage.User{}, storage.ErrForbidden
	}
	return s.store.UserByID(ctx, id)
}
func (s *Server) addManagedUser(ctx context.Context, actor storage.User, email, role string) (storage.User, error) {
	if actor.Role != storage.UserRoleAdmin {
		return storage.User{}, storage.ErrForbidden
	}
	return s.store.UpsertAllowedUser(ctx, email, role)
}
func (s *Server) setManagedUserStatus(ctx context.Context, actor storage.User, id, status string) error {
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
	return s.store.SetUserStatus(ctx, id, status)
}
func (s *Server) setManagedUserRole(ctx context.Context, actor storage.User, id, role string) error {
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
	return s.store.SetUserRole(ctx, id, role)
}
func (s *Server) deleteManagedUser(ctx context.Context, actor storage.User, id string) error {
	target, err := s.managedUser(ctx, actor, id)
	if err != nil {
		return err
	}
	if target.ID == actor.ID || target.Role == storage.UserRoleAdmin {
		return fmt.Errorf("%w: admin users cannot be deleted", storage.ErrBadRequest)
	}
	return s.store.DeleteUser(ctx, id)
}
