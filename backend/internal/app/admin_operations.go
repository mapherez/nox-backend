package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/mapherez/nox-backend/backend/internal/storage"
	noxmcp "github.com/mapherez/nox-mcp/go"
)

func (s *Server) webUser(r *http.Request) (storage.User, bool, error) {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil {
		return storage.User{}, false, nil
	}
	return s.store.UserBySessionToken(r.Context(), cookie.Value)
}

func (s *Server) configuredAdmin(email string) bool {
	for _, configured := range s.cfg.AdminEmails {
		if strings.EqualFold(strings.TrimSpace(configured), strings.TrimSpace(email)) {
			return true
		}
	}
	return false
}
func (s *Server) isAdminManager(user storage.User) bool {
	return user.Role == storage.UserRoleAdmin && user.Status == storage.UserStatusActive && s.configuredAdmin(user.Email)
}
func (s *Server) canManageUser(actor, target storage.User) bool {
	return actor.Role == storage.UserRoleAdmin && actor.Status == storage.UserStatusActive && actor.ID != target.ID && !s.configuredAdmin(target.Email) && (target.Role != storage.UserRoleAdmin || s.isAdminManager(actor))
}
func (s *Server) userManagementPolicy(actor storage.User) storage.UserManagementPolicy {
	return storage.UserManagementPolicy{ActorID: actor.ID, CanManageAdmins: s.isAdminManager(actor), ProtectedEmails: s.cfg.AdminEmails}
}
func protectedUserError() error {
	return fmt.Errorf("%w: this account is protected; only a configured Admin Manager can manage other admins, and configured manager accounts are managed through deployment configuration", storage.ErrBadRequest)
}
func (s *Server) managedUser(ctx context.Context, actor storage.User, id string) (storage.User, error) {
	if actor.Role != storage.UserRoleAdmin || actor.Status != storage.UserStatusActive {
		return storage.User{}, storage.ErrForbidden
	}
	target, err := s.store.UserByID(ctx, id)
	if err != nil {
		return storage.User{}, err
	}
	if !s.canManageUser(actor, target) {
		return storage.User{}, protectedUserError()
	}
	return target, nil
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
	if s.configuredAdmin(email) {
		return storage.User{}, protectedUserError()
	}
	existing, err := s.store.UserByEmail(ctx, email)
	if err != nil && !errors.Is(err, storage.ErrNotFound) {
		return storage.User{}, err
	}
	if err == nil && !s.canManageUser(actor, existing) {
		return storage.User{}, protectedUserError()
	}
	return s.store.UpsertManagedUser(ctx, email, role, s.userManagementPolicy(actor))
}
func (s *Server) setManagedUserStatus(ctx context.Context, actor storage.User, id, status string) error {
	status = strings.ToUpper(strings.TrimSpace(status))
	if _, err := s.managedUser(ctx, actor, id); err != nil {
		return err
	}
	return s.store.UpdateManagedUser(ctx, id, "status", status, s.userManagementPolicy(actor))
}
func (s *Server) setManagedUserRole(ctx context.Context, actor storage.User, id, role string) error {
	role = strings.ToUpper(strings.TrimSpace(role))
	if role != storage.UserRoleUser && role != storage.UserRoleAdmin {
		return fmt.Errorf("%w: role must be USER or ADMIN", storage.ErrBadRequest)
	}
	if _, err := s.managedUser(ctx, actor, id); err != nil {
		return err
	}
	return s.store.UpdateManagedUser(ctx, id, "role", role, s.userManagementPolicy(actor))
}
func (s *Server) deleteManagedUser(ctx context.Context, actor storage.User, id string) error {
	if _, err := s.managedUser(ctx, actor, id); err != nil {
		return err
	}
	return s.store.DeleteManagedUser(ctx, id, s.userManagementPolicy(actor))
}
func (s *Server) saveFoundationConnection(ctx context.Context, actor storage.User, service, origin, revision string) (storage.FoundationConnection, error) {
	if actor.Role != storage.UserRoleAdmin || actor.Status != storage.UserStatusActive {
		return storage.FoundationConnection{}, storage.ErrForbidden
	}
	origin, err := noxmcp.NormalizeSuiteOrigin(strings.TrimSpace(origin))
	if err != nil {
		return storage.FoundationConnection{}, fmt.Errorf("%w: origin must be an HTTP or HTTPS IP/hostname with optional port, without a base path, credentials, query or fragment", storage.ErrBadRequest)
	}
	return s.store.SaveFoundationConnection(ctx, service, origin, revision)
}
