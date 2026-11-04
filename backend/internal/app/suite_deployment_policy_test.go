package app

import (
	"encoding/json"
	"fmt"
	"testing"
)

func TestSuiteInstallerDeploymentHTTPUsesEnrolledIdentityAndExactScope(t *testing.T) {
	f := suiteHTTPServer(t)
	installer, _ := f.paired(t, "local-installer", "installer")
	other, _ := f.paired(t, "other-installer", "installer")
	path := fmt.Sprintf("/api/nox/services/%s/deployments/own-job", installer.Service.ServiceID)
	body := map[string]string{"productId": "future-product", "baseUrl": "http://future-product"}
	status, _, raw := f.call(t, "PUT", path, body, installer.ServiceCredential, map[string]string{"If-None-Match": "*"})
	suiteHTTPStatus(t, status, 200, raw)
	var grant struct {
		Revision string `json:"revision"`
	}
	if err := json.Unmarshal(raw, &grant); err != nil || grant.Revision == "" {
		t.Fatal("missing grant revision", err)
	}
	status, _, raw = f.call(t, "PUT", path, body, other.ServiceCredential, map[string]string{"If-Match": `"` + grant.Revision + `"`})
	suiteHTTPStatus(t, status, 403, raw)
	status, _, raw = f.call(t, "DELETE", path, nil, other.ServiceCredential, map[string]string{"If-Match": `"` + grant.Revision + `"`})
	suiteHTTPStatus(t, status, 403, raw)
	status, _, raw = f.call(t, "DELETE", path, nil, installer.ServiceCredential, map[string]string{"If-Match": `"` + grant.Revision + `"`})
	suiteHTTPStatus(t, status, 200, raw)
}
