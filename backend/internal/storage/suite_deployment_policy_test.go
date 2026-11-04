package storage

import (
	"context"
	"sync"
	"testing"

	noxmcp "github.com/mapherez/nox-mcp/go"
)

func TestPairedInstallerManagesOnlyItsLocalDeployments(t *testing.T) {
	f := newTrustFixture(t)
	ctx := context.Background()
	installer := f.provision(t, "local-installer", "installer", true)
	other := f.provision(t, "other-installer", "installer", true)
	gateway := f.provision(t, "local-router", "gateway", true)
	caller := SuiteCaller{ServiceToken: installer.ServiceCredential}
	grant := &SuiteDeploymentGrant{ProductID: "local-domain", BaseURL: "http://local-domain"}
	result, err := f.store.SuiteSetDeployment(ctx, caller, installer.Service.ServiceID, "deployment-one", "", "create-local", "create-local", grant)
	if err != nil {
		t.Fatal(err)
	}
	view := trustDecode[struct {
		Revision string `json:"revision"`
	}](t, result, nil)
	replay, err := f.store.SuiteSetDeployment(ctx, caller, installer.Service.ServiceID, "deployment-one", "", "create-local", "create-local", grant)
	if err != nil || string(replay.Body) != string(result.Body) {
		t.Fatal("lost response not replayable", err)
	}
	for _, test := range []struct {
		caller  SuiteCaller
		service string
		body    *SuiteDeploymentGrant
	}{
		{caller, other.Service.ServiceID, grant},
		{SuiteCaller{ServiceToken: gateway.ServiceCredential}, installer.Service.ServiceID, grant},
		{caller, installer.Service.ServiceID, &SuiteDeploymentGrant{ProductID: "remote-domain", BaseURL: "https://remote.example.test"}},
		{caller, installer.Service.ServiceID, &SuiteDeploymentGrant{ProductID: installer.Service.ProductID, BaseURL: "http://" + installer.Service.ProductID}},
	} {
		_, err = f.store.SuiteSetDeployment(ctx, test.caller, test.service, "denied", "", "denied-"+test.service+test.body.ProductID, "denied", test.body)
		trustExpectedError(t, err, 403)
	}
	changed := &SuiteDeploymentGrant{ProductID: "substituted", BaseURL: "http://substituted"}
	_, err = f.store.SuiteSetDeployment(ctx, caller, installer.Service.ServiceID, "deployment-one", view.Revision, "change-target", "change-target", changed)
	trustExpectedError(t, err, 403)
	_, err = f.store.SuiteSetDeployment(ctx, SuiteCaller{ServiceToken: other.ServiceCredential}, other.Service.ServiceID, "another", "", "claim", "claim", grant)
	trustExpectedError(t, err, 409)
	_, err = f.store.SuiteSetDeployment(ctx, caller, installer.Service.ServiceID, "deployment-one", view.Revision, "remove-local", "remove-local", nil)
	if err != nil {
		t.Fatal(err)
	}
	req := trustRequest(grant.ProductID, "product", grant.BaseURL)
	req.ManagedBy = &noxmcp.SuiteRegistrationRecordManagedBy{ProductID: installer.Service.ProductID, DeploymentID: "deployment-one"}
	_, err = f.store.SuiteCreatePairing(ctx, caller, req, "", "after-removal", "after-removal")
	trustExpectedError(t, err, 403)
}
func TestInstallerCannotClaimStandaloneOrForeignAssociation(t *testing.T) {
	f := newTrustFixture(t)
	ctx := context.Background()
	installer := f.provision(t, "local-installer", "installer", true)
	f.provision(t, "standalone-domain", "product", true)
	_, err := f.store.SuiteSetDeployment(ctx, SuiteCaller{ServiceToken: installer.ServiceCredential}, installer.Service.ServiceID, "claim-standalone", "", "claim", "claim", &SuiteDeploymentGrant{ProductID: "standalone-domain", BaseURL: "http://standalone-domain"})
	trustExpectedError(t, err, 403)
}
func TestConcurrentInstallersCannotClaimOneProduct(t *testing.T) {
	f := newTrustFixture(t)
	ctx := context.Background()
	installers := []noxmcp.SuiteProvisioning{f.provision(t, "installer-one", "installer", true), f.provision(t, "installer-two", "installer", true)}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, installer := range installers {
		wg.Add(1)
		go func(installer noxmcp.SuiteProvisioning) {
			defer wg.Done()
			_, err := f.store.SuiteSetDeployment(ctx, SuiteCaller{ServiceToken: installer.ServiceCredential}, installer.Service.ServiceID, "local-job", "", "claim", "claim", &SuiteDeploymentGrant{ProductID: "one-domain", BaseURL: "http://one-domain"})
			results <- err
		}(installer)
	}
	wg.Wait()
	close(results)
	successes := 0
	for err := range results {
		if err == nil {
			successes++
		} else {
			trustExpectedError(t, err, 409)
		}
	}
	if successes != 1 {
		t.Fatal("product received multiple owners", successes)
	}
}
