package storage

import (
	"context"
	"encoding/json"
	"time"

	noxmcp "github.com/mapherez/nox-mcp/go"
)

// Native administration is a safe projection, not a second Suite wire contract.
// Private records/verifiers/secret handles never leave storage through this view.
type SuiteAdminPairing struct {
	PairingID string `json:"pairingId"`
	ProductID string `json:"productId"`
	BaseURL   string `json:"baseUrl"`
	Purpose   string `json:"purpose"`
	State     string `json:"state"`
	Revision  string `json:"revision"`
	ExpiresAt string `json:"expiresAt"`
	ServiceID string `json:"serviceId,omitempty"`
}
type SuiteAdminCredential struct {
	CredentialID string `json:"credentialId"`
	Revision     string `json:"revision"`
	State        string `json:"state"`
	ExpiresAt    string `json:"expiresAt,omitempty"`
	Current      bool   `json:"current"`
}
type SuiteAdminDeployment struct {
	DeploymentID string `json:"deploymentId"`
	ProductID    string `json:"productId"`
	BaseURL      string `json:"baseUrl"`
	Revision     string `json:"revision"`
}
type SuiteAdminService struct {
	ServiceID     string                 `json:"serviceId"`
	ProductID     string                 `json:"productId"`
	AssociationID string                 `json:"associationId"`
	BaseURL       string                 `json:"baseUrl"`
	Purpose       string                 `json:"purpose"`
	State         string                 `json:"state"`
	Revision      string                 `json:"revision"`
	HeadRevision  string                 `json:"headRevision"`
	Current       bool                   `json:"current"`
	Credentials   []SuiteAdminCredential `json:"credentials"`
	Deployments   []SuiteAdminDeployment `json:"deployments"`
}
type SuiteAdminSigningKey struct {
	KeyID     string `json:"keyId"`
	State     string `json:"state"`
	RetiresAt string `json:"retiresAt,omitempty"`
}
type SuiteAdministration struct {
	Issuer           string                            `json:"issuer"`
	KeyRevision      string                            `json:"keyRevision"`
	RegistryRevision string                            `json:"registryRevision"`
	ObservedAt       string                            `json:"observedAt"`
	SigningKeys      []SuiteAdminSigningKey            `json:"signingKeys"`
	Pairings         []SuiteAdminPairing               `json:"pairings"`
	Services         []SuiteAdminService               `json:"services"`
	Products         []noxmcp.SuiteRegistrationRecord  `json:"products"`
	Observations     []noxmcp.SuiteObservationEnvelope `json:"observations"`
	BlockedProducts  []string                          `json:"blockedProducts"`
}

func (s *Store) SuiteAdministration(ctx context.Context, caller SuiteCaller) (SuiteAdministration, error) {
	out := SuiteAdministration{SigningKeys: []SuiteAdminSigningKey{}, Pairings: []SuiteAdminPairing{}, Services: []SuiteAdminService{}, Products: []noxmcp.SuiteRegistrationRecord{}, Observations: []noxmcp.SuiteObservationEnvelope{}, BlockedProducts: []string{}}
	_, err := s.suiteTransaction(ctx, func(x *suiteTx) (SuiteResult, error) {
		actor, err := x.actor(caller)
		if err != nil {
			return SuiteResult{}, err
		}
		if !actor.admin() {
			return SuiteResult{}, suiteDenied()
		}
		if err = x.maintain(); err != nil {
			return SuiteResult{}, err
		}
		out.ObservedAt = suiteTime(x.now)
		if err = x.tx.QueryRowContext(ctx, `SELECT issuer,revision FROM suite_authority WHERE singleton=1`).Scan(&out.Issuer, &out.KeyRevision); err != nil {
			return SuiteResult{}, err
		}
		if err = x.tx.QueryRowContext(ctx, `SELECT revision FROM suite_registry_state WHERE singleton=1`).Scan(&out.RegistryRevision); err != nil {
			return SuiteResult{}, err
		}
		rows, err := x.tx.QueryContext(ctx, `SELECT id,state,retire_at FROM suite_signing_keys ORDER BY created_at,id`)
		if err != nil {
			return SuiteResult{}, err
		}
		for rows.Next() {
			var k SuiteAdminSigningKey
			var expiry int64
			if err = rows.Scan(&k.KeyID, &k.State, &expiry); err != nil {
				break
			}
			if expiry != 0 {
				k.RetiresAt = suiteTime(time.UnixMilli(expiry))
			}
			out.SigningKeys = append(out.SigningKeys, k)
		}
		if err == nil {
			err = rows.Err()
		}
		rows.Close()
		if err != nil {
			return SuiteResult{}, err
		}
		ids, err := x.ids(`SELECT id FROM suite_pairings WHERE state IN ('pending','provisioned') OR id IN (SELECT id FROM suite_pairings ORDER BY expires_at DESC,id DESC LIMIT 50) ORDER BY expires_at DESC,id`)
		if err != nil {
			return SuiteResult{}, err
		}
		for _, id := range ids {
			p, err := x.pairing(id)
			if err != nil {
				return SuiteResult{}, err
			}
			out.Pairings = append(out.Pairings, SuiteAdminPairing{p.ID, p.Request.ProductID, p.Request.BaseUrl, p.Request.Purpose, p.State, p.Revision, suiteTime(time.UnixMilli(p.ExpiresAt)), p.ServiceID})
		}
		ids, err = x.ids(`SELECT id FROM suite_services WHERE id IN (SELECT service_id FROM suite_trust_heads) OR state='provisioned' ORDER BY product_id,id`)
		if err != nil {
			return SuiteResult{}, err
		}
		for _, id := range ids {
			svc, err := x.service(id)
			if err != nil {
				return SuiteResult{}, err
			}
			head, revision, err := x.head(svc.ProductID)
			if err != nil {
				return SuiteResult{}, err
			}
			v := SuiteAdminService{ServiceID: svc.ID, ProductID: svc.ProductID, AssociationID: svc.AssociationID, BaseURL: svc.Origin, Purpose: svc.Purpose, State: svc.State, Revision: svc.Revision, HeadRevision: revision, Current: head == svc.ID, Credentials: []SuiteAdminCredential{}, Deployments: []SuiteAdminDeployment{}}
			creds, err := x.ids(`SELECT id FROM suite_credentials WHERE service_id=? AND (state<>'revoked' OR id=?) ORDER BY id`, id, svc.CurrentCredentialID)
			if err != nil {
				return SuiteResult{}, err
			}
			for _, cid := range creds {
				c, err := x.credential(cid)
				if err != nil {
					return SuiteResult{}, err
				}
				item := SuiteAdminCredential{CredentialID: c.ID, Revision: c.Revision, State: c.State, Current: c.ID == svc.CurrentCredentialID}
				if c.NotAfter != 0 {
					item.ExpiresAt = suiteTime(time.UnixMilli(c.NotAfter))
				}
				v.Credentials = append(v.Credentials, item)
			}
			rows, err := x.tx.QueryContext(ctx, `SELECT deployment_id,product_id,origin,revision FROM suite_deployments WHERE service_id=? ORDER BY deployment_id`, id)
			if err != nil {
				return SuiteResult{}, err
			}
			for rows.Next() {
				var d SuiteAdminDeployment
				if err = rows.Scan(&d.DeploymentID, &d.ProductID, &d.BaseURL, &d.Revision); err != nil {
					break
				}
				v.Deployments = append(v.Deployments, d)
			}
			if err == nil {
				err = rows.Err()
			}
			rows.Close()
			if err != nil {
				return SuiteResult{}, err
			}
			out.Services = append(out.Services, v)
		}
		ids, err = x.ids(`SELECT product_id FROM suite_products ORDER BY product_id`)
		if err != nil {
			return SuiteResult{}, err
		}
		for _, id := range ids {
			record, err := x.product(id)
			if err != nil {
				return SuiteResult{}, err
			}
			out.Products = append(out.Products, record)
			observations, err := x.observationEnvelopes(id)
			if err != nil {
				return SuiteResult{}, err
			}
			out.Observations = append(out.Observations, observations...)
		}
		out.BlockedProducts, err = x.ids(`SELECT a.product_id FROM suite_product_admission a JOIN suite_products p ON p.product_id=a.product_id AND p.revision=a.revision AND p.association_id=a.association_id WHERE a.blocked=1 ORDER BY a.product_id`)
		if err != nil {
			return SuiteResult{}, err
		}
		raw, err := json.Marshal(out)
		if err != nil {
			return SuiteResult{}, err
		}
		if len(raw) > noxmcp.SuiteMaxPayloadBytes {
			return SuiteResult{}, suiteError(503, "INTEGRATION_UNAVAILABLE", "The native administration snapshot exceeds its bound. Use the scoped management APIs to reconcile history.")
		}
		return SuiteResult{}, nil
	})
	return out, err
}
