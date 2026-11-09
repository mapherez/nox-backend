package storage

import (
	"context"
	"encoding/json"
	"errors"
)

const backendPublicationProduct = "nox-backend"
const backendPublicationOrigin = "http://nox-backend"
const backendPublicationMarker = "nox/backend-publication"

// Metadata selects interrupted workflow attempts, never grants permission.
func (s *Store) SuiteBackendPublicationPairings(ctx context.Context, caller SuiteCaller) ([]SuiteAdminPairing, error) {
	out := []SuiteAdminPairing{}
	_, err := s.suiteTransaction(ctx, func(x *suiteTx) (SuiteResult, error) {
		actor, err := x.actor(caller)
		if err != nil {
			return SuiteResult{}, err
		}
		if !actor.admin() {
			return SuiteResult{}, suiteDenied()
		}
		ids, err := x.ids(`SELECT id FROM suite_pairings WHERE product_id=? AND state IN ('pending','provisioned')`, backendPublicationProduct)
		if err != nil {
			return SuiteResult{}, err
		}
		for _, id := range ids {
			p, err := x.pairing(id)
			if err != nil {
				return SuiteResult{}, err
			}
			if p.Request.BaseUrl != backendPublicationOrigin || p.Request.Purpose != "product" || p.Request.ManagedBy != nil || string(p.Request.Extra[backendPublicationMarker]) != "true" {
				continue
			}
			out = append(out, SuiteAdminPairing{PairingID: p.ID, Revision: p.Revision})
		}
		return SuiteResult{}, nil
	})
	return out, err
}

// Restart recovery uses the actual administrator who created this product's
// acknowledged association. It never chooses an arbitrary user or service actor.
func (s *Store) SuiteBackendPublicationActor(ctx context.Context) (User, error) {
	var user User
	_, err := s.suiteTransaction(ctx, func(x *suiteTx) (SuiteResult, error) {
		head, _, err := x.head(backendPublicationProduct)
		if err != nil || head == "" {
			return SuiteResult{}, err
		}
		service, err := x.service(head)
		if err != nil {
			return SuiteResult{}, err
		}
		if service.State != "active" || service.Purpose != "product" || service.Origin != backendPublicationOrigin || service.ManagedBy != nil {
			return SuiteResult{}, nil
		}
		record, err := x.product(backendPublicationProduct)
		if err != nil {
			var failure *SuiteError
			if !errors.As(err, &failure) || failure.Code != "PRODUCT_NOT_REGISTERED" {
				return SuiteResult{}, err
			}
		} else if record.AssociationID != service.AssociationID || record.ManagedBy != nil || record.Connection.BaseUrl != backendPublicationOrigin {
			return SuiteResult{}, nil
		}
		var extensions map[string]json.RawMessage
		markedRecord := err == nil && json.Unmarshal(record.Extra["requestExtensions"], &extensions) == nil && string(extensions[backendPublicationMarker]) == "true"
		ids, err := x.ids(`SELECT id FROM suite_pairings WHERE product_id=? AND state='acknowledged'`, backendPublicationProduct)
		if err != nil {
			return SuiteResult{}, err
		}
		for _, id := range ids {
			p, err := x.pairing(id)
			if err != nil {
				return SuiteResult{}, err
			}
			if p.ServiceID != head || p.CreatorUserID == "" || (!markedRecord && string(p.Request.Extra[backendPublicationMarker]) != "true") {
				continue
			}
			actor, err := x.actor(SuiteCaller{UserID: p.CreatorUserID})
			if err == nil && actor.admin() {
				user = *actor.user
			}
		}
		return SuiteResult{}, nil
	})
	return user, err
}
