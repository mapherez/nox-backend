package storage

import "context"

// Central admission changes invalidate issued authorization metadata without
// rewriting local user roles, keys or the application's enabled/disabled state.
func (s *Store) InvalidateNoXAdmission(ctx context.Context, userID string) error {
	_, err := s.suiteTransaction(ctx, func(x *suiteTx) (SuiteResult, error) {
		revision, e := randomID("authorization_")
		if e != nil {
			return SuiteResult{}, e
		}
		if _, e = x.tx.ExecContext(ctx, `UPDATE suite_user_authority SET revision=? WHERE user_id=?`, revision, userID); e != nil {
			return SuiteResult{}, e
		}
		return SuiteResult{}, x.event("authorization.invalidated", map[string]any{"userId": userID})
	})
	return err
}
