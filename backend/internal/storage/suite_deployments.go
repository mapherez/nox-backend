package storage

import (
	"context"
	"database/sql"
	"errors"

	noxmcp "github.com/mapherez/nox-mcp/go"
)

type SuiteDeploymentGrant struct {
	ProductID string `json:"productId"`
	BaseURL   string `json:"baseUrl"`
}

func (s *Store) SuiteSetDeployment(ctx context.Context, caller SuiteCaller, serviceID, deploymentID, expected, key, digest string, grant *SuiteDeploymentGrant) (SuiteResult, error) {
	return s.suiteMutation(ctx, caller, "deployments.manage:"+serviceID+":"+deploymentID, key, digest, suiteDeploymentManager(serviceID, deploymentID, grant), func(x *suiteTx, _ suiteActor) (SuiteResult, error) {
		service, err := x.service(serviceID)
		if err != nil {
			return SuiteResult{}, err
		}
		if service.Purpose != "installer" || service.State != "active" {
			return SuiteResult{}, suiteDenied()
		}
		if len(deploymentID) < 1 || len(deploymentID) > 256 {
			return SuiteResult{}, suiteError(400, "INVALID_REGISTRATION", "A bounded deployment identifier is required.")
		}
		var current string
		err = x.tx.QueryRowContext(ctx, `SELECT revision FROM suite_deployments WHERE service_id=? AND deployment_id=?`, serviceID, deploymentID).Scan(&current)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return SuiteResult{}, err
		}
		if expected != current {
			return SuiteResult{}, suitePrecondition()
		}
		if grant == nil {
			if current == "" {
				return SuiteResult{}, suitePrecondition()
			}
			if _, err := x.tx.ExecContext(ctx, `DELETE FROM suite_deployments WHERE service_id=? AND deployment_id=?`, serviceID, deploymentID); err != nil {
				return SuiteResult{}, err
			}
			return suiteResponse(200, map[string]bool{"removed": true})
		}
		if !noxmcp.ValidSuiteProductID(grant.ProductID) {
			return SuiteResult{}, suiteError(400, "INVALID_REGISTRATION", "Invalid product identifier.")
		}
		origin, err := noxmcp.NormalizeSuiteOrigin(grant.BaseURL)
		if err != nil {
			return SuiteResult{}, suiteError(400, "INVALID_REGISTRATION", "An approved HTTP(S) origin is required.")
		}
		revision, err := randomID("deployment_revision_")
		if err != nil {
			return SuiteResult{}, err
		}
		if _, err := x.tx.ExecContext(ctx, `INSERT INTO suite_deployments VALUES (?,?,?,?,?) ON CONFLICT(service_id,deployment_id) DO UPDATE SET product_id=excluded.product_id,origin=excluded.origin,revision=excluded.revision`, serviceID, deploymentID, grant.ProductID, origin, revision); err != nil {
			return SuiteResult{}, err
		}
		_, trustRevision, err := x.head(grant.ProductID)
		if err != nil {
			return SuiteResult{}, err
		}
		return suiteResponse(200, map[string]any{"serviceId": serviceID, "deploymentId": deploymentID, "productId": grant.ProductID, "baseUrl": origin, "revision": revision, "trustRevision": trustRevision})
	})
}

func (s *Store) SuiteReadDeployments(ctx context.Context, caller SuiteCaller, id string) (SuiteResult, error) {
	return s.suiteTransaction(ctx, func(x *suiteTx) (SuiteResult, error) {
		actor, err := x.actor(caller)
		if err != nil {
			return SuiteResult{}, err
		}
		if !actor.admin() && (actor.service == nil || actor.service.ID != id || !actor.grant("pairing.manage-associated")) {
			return SuiteResult{}, suiteDenied()
		}
		rows, err := x.tx.QueryContext(ctx, `SELECT deployment_id,product_id,origin,revision FROM suite_deployments WHERE service_id=? ORDER BY deployment_id`, id)
		if err != nil {
			return SuiteResult{}, err
		}
		defer rows.Close()
		result := []map[string]string{}
		for rows.Next() {
			var deployment, product, origin, revision string
			if err := rows.Scan(&deployment, &product, &origin, &revision); err != nil {
				return SuiteResult{}, err
			}
			result = append(result, map[string]string{"deploymentId": deployment, "productId": product, "baseUrl": origin, "revision": revision})
		}
		if err := rows.Err(); err != nil {
			return SuiteResult{}, err
		}
		if err := rows.Close(); err != nil {
			return SuiteResult{}, err
		}
		for _, item := range result {
			_, revision, err := x.head(item["productId"])
			if err != nil {
				return SuiteResult{}, err
			}
			item["trustRevision"] = revision
		}
		return suiteResponse(200, result)
	})
}
