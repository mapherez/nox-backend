package storage

import (
	"database/sql"
	"errors"
)

// Administrative pairing delegates associated deployment management to the
// authenticated installer. It grants no account/key administration and cannot
// be used for another service, remote origin or another installer's product.
func suiteDeploymentManager(serviceID, deploymentID string, grant *SuiteDeploymentGrant) func(*suiteTx, suiteActor) error {
	return func(x *suiteTx, a suiteActor) error {
		if a.admin() {
			return nil
		}
		if a.service == nil || a.service.Purpose != "installer" || a.service.ID != serviceID || !a.grant("pairing.manage-associated") || !a.grant("registry.manage-associated") || !a.grant("lifecycle.report") {
			return suiteDenied()
		}
		current, _, err := x.head(a.service.ProductID)
		if err != nil {
			return err
		}
		if current != serviceID {
			return suiteDenied()
		}
		if grant == nil {
			return nil
		} // The row is selected by this exact service/deployment.
		if grant.ProductID == a.service.ProductID || grant.BaseURL != suiteDeploymentOrigin(grant.ProductID) {
			return suiteDenied()
		}
		var product, origin string
		err = x.tx.QueryRowContext(x.ctx, `SELECT product_id,origin FROM suite_deployments WHERE service_id=? AND deployment_id=?`, serviceID, deploymentID).Scan(&product, &origin)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err == nil && (product != grant.ProductID || origin != grant.BaseURL) {
			return suiteDenied()
		}
		var claims int
		if err = x.tx.QueryRowContext(x.ctx, `SELECT count(*) FROM suite_deployments WHERE product_id=? AND NOT(service_id=? AND deployment_id=?)`, grant.ProductID, serviceID, deploymentID).Scan(&claims); err != nil {
			return err
		}
		if claims != 0 {
			return suiteConflict()
		}
		head, _, err := x.head(grant.ProductID)
		if err != nil {
			return err
		}
		if head != "" {
			target, err := x.service(head)
			if err != nil {
				return err
			}
			if target.Purpose != "product" || target.ManagedBy == nil || target.ManagedBy.ProductID != a.service.ProductID {
				return suiteDenied()
			}
			if target.ManagedBy.DeploymentID != deploymentID {
				// Reinstallation can replace only an association whose own previous grant
				// has already been retired. It cannot take over a live deployment.
				var old int
				if err = x.tx.QueryRowContext(x.ctx, `SELECT count(*) FROM suite_deployments WHERE service_id=? AND deployment_id=?`, serviceID, target.ManagedBy.DeploymentID).Scan(&old); err != nil {
					return err
				}
				if old != 0 {
					return suiteConflict()
				}
			}
		}
		record, err := x.product(grant.ProductID)
		if err == nil {
			if record.ManagedBy == nil || record.ManagedBy.ProductID != a.service.ProductID || record.ManagedBy.DeploymentID != deploymentID {
				return suiteDenied()
			}
		} else {
			var failure *SuiteError
			if !errors.As(err, &failure) || failure.Status != 404 {
				return err
			}
		}
		return nil
	}
}

func suiteDeploymentOrigin(id string) string {
	host := id
	numeric := id != ""
	for _, c := range id {
		if c < '0' || c > '9' {
			numeric = false
			break
		}
	}
	if len(id) > 63 || numeric {
		if len(id) > 32 {
			host = id[:32] + "." + id[32:]
		}
		host += ".nox-suite"
	}
	return "http://" + host
}
