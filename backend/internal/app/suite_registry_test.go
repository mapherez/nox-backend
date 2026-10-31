package app

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	noxmcp "github.com/mapherez/nox-mcp/go"
)

func (f *suiteHTTPFixture) paired(t *testing.T, id, purpose string) (noxmcp.SuiteProvisioning, string) {
	t.Helper()
	product, decl, pin := f.product(t, id, nil)
	intent := f.create(t, id, purpose, product.URL)
	proof := suiteHTTPProof(t, intent, decl, pin)
	status, _, raw := f.call(t, "POST", "/api/nox/pairings/"+intent.PairingID+"/complete", proof, "", nil)
	suiteHTTPStatus(t, status, 200, raw)
	p, err := noxmcp.LoadSuiteProvisioning(raw)
	if err != nil {
		t.Fatal(err)
	}
	status, _, raw = f.call(t, "POST", "/api/nox/pairings/"+intent.PairingID+"/ack", nil, p.ServiceCredential, nil)
	suiteHTTPStatus(t, status, 200, raw)
	return p, product.URL
}
func TestSuiteHTTPSRegistryVerificationReplayValidatorsAndMemberViews(t *testing.T) {
	f := suiteHTTPServer(t)
	var reads atomic.Int64
	var current atomic.Value
	product, decl, pin := f.product(t, "http-catalog", func(w http.ResponseWriter, r *http.Request) {
		reads.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(current.Load().([]byte))
	})
	current.Store([]byte(decl))
	intent := f.create(t, "http-catalog", "product", product.URL)
	proof := suiteHTTPProof(t, intent, decl, pin)
	status, _, raw := f.call(t, "POST", "/api/nox/pairings/"+intent.PairingID+"/complete", proof, "", nil)
	suiteHTTPStatus(t, status, 200, raw)
	p, err := noxmcp.LoadSuiteProvisioning(raw)
	if err != nil {
		t.Fatal(err)
	}
	status, _, raw = f.call(t, "POST", "/api/nox/pairings/"+intent.PairingID+"/ack", nil, p.ServiceCredential, nil)
	suiteHTTPStatus(t, status, 200, raw)
	body := map[string]any{"contractVersion": "1.0", "baseUrl": product.URL}
	path := "/api/nox/products/http-catalog"
	status, _, raw = f.call(t, "PUT", path, body, f.adminKey, nil)
	suiteHTTPStatus(t, status, 428, raw)
	headers := map[string]string{"If-None-Match": "*", "Idempotency-Key": "registry-enroll"}
	status, h, first := f.call(t, "PUT", path, body, f.adminKey, headers)
	suiteHTTPStatus(t, status, 201, first)
	if h.Get("ETag") != "" || h.Get("Last-Modified") != "" {
		t.Fatal("enriched PUT improperly returned a validator")
	}
	record, err := noxmcp.LoadSuiteRegistrationRecord(first)
	if err != nil {
		t.Fatal(err)
	}
	if reads.Load() != 2 {
		t.Fatal("enrollment did not verify declaration again")
	}
	status, _, raw = f.call(t, "PUT", path, body, f.adminKey, headers)
	suiteHTTPStatus(t, status, 201, raw)
	if !bytes.Equal(first, raw) || reads.Load() != 2 {
		t.Fatal("idempotent replay refetched or mutated declaration")
	}
	status, h, raw = f.call(t, "GET", path, nil, f.adminKey, nil)
	suiteHTTPStatus(t, status, 200, raw)
	if h.Get("ETag") != `"`+record.Revision+`"` {
		t.Fatal("stable admin representation lacks revision ETag")
	}
	member, err := f.store.UpsertAllowedUser(context.Background(), "registry-member@example.com", "USER")
	if err != nil {
		t.Fatal(err)
	}
	memberKey, err := f.store.CurrentAPIKey(context.Background(), member.ID)
	if err != nil {
		t.Fatal(err)
	}
	status, h, raw = f.call(t, "GET", path, nil, memberKey, nil)
	suiteHTTPStatus(t, status, 200, raw)
	if h.Get("ETag") != "" {
		t.Fatal("member reused privileged ETag")
	}
	for _, leak := range []string{"baseUrl", "associationId", "optionalExtension", "cursor"} {
		if strings.Contains(string(raw), leak) {
			t.Fatal("member received internal registry details", leak)
		}
	}
	status, _, raw = f.call(t, "PUT", path, body, memberKey, headers)
	suiteHTTPStatus(t, status, 403, raw)
	// A changed identity cannot replace membership, even on the same approved TLS key.
	current.Store(bytes.Replace(decl, []byte(`"http-catalog"`), []byte(`"impostor"`), 1))
	status, _, raw = f.call(t, "PUT", path, body, f.adminKey, map[string]string{"If-Match": `"` + record.Revision + `"`})
	suiteHTTPStatus(t, status, 409, raw)
	current.Store(bytes.Replace(decl, []byte(`"productVersion":"fixture"`), []byte(`"productVersion":"updated"`), 1))
	status, _, raw = f.call(t, "PUT", path, body, f.adminKey, map[string]string{"If-Match": `"` + record.Revision + `"`})
	suiteHTTPStatus(t, status, 200, raw)
	updated, err := noxmcp.LoadSuiteRegistrationRecord(raw)
	if err != nil || updated.Revision == record.Revision {
		t.Fatal("verified metadata did not advance revision", err)
	}
	product.Close()
	// A committed retry remains available when the participant is now unreachable.
	status, _, raw = f.call(t, "PUT", path, body, f.adminKey, headers)
	suiteHTTPStatus(t, status, 201, raw)
	if !bytes.Equal(first, raw) {
		t.Fatal("offline retry lost original result")
	}
	status, _, raw = f.call(t, "PUT", path, body, f.adminKey, map[string]string{"If-Match": `"` + updated.Revision + `"`})
	suiteHTTPStatus(t, status, 503, raw)
	status, _, raw = f.call(t, "DELETE", path, nil, f.adminKey, map[string]string{"If-Match": `"` + updated.Revision + `"`, "Idempotency-Key": "remove-http"})
	if status != 204 || len(raw) != 0 {
		t.Fatalf("DELETE must return empty 204: %d %s", status, raw)
	}
	status, _, raw = f.call(t, "DELETE", path, nil, f.adminKey, map[string]string{"If-Match": `"` + updated.Revision + `"`, "Idempotency-Key": "remove-http"})
	if status != 204 {
		t.Fatal("removal replay failed", status, string(raw))
	}
	status, _, raw = f.call(t, "GET", path, nil, f.adminKey, nil)
	suiteHTTPStatus(t, status, 404, raw)
}
func TestSuiteHTTPSRegistrySnapshotsEventsAndOrderedReports(t *testing.T) {
	f := suiteHTTPServer(t)
	gateway, _ := f.paired(t, "http-router", "gateway")
	participant, origin := f.paired(t, "reported-catalog", "product")
	status, _, raw := f.call(t, "GET", "/api/nox/products", nil, gateway.ServiceCredential, nil)
	suiteHTTPStatus(t, status, 200, raw)
	before, err := noxmcp.LoadSuiteRegistrySnapshot(raw)
	if err != nil {
		t.Fatal(err)
	}
	status, _, raw = f.call(t, "PUT", "/api/nox/products/reported-catalog", map[string]any{"contractVersion": "1.0", "baseUrl": origin}, f.adminKey, map[string]string{"If-None-Match": "*"})
	suiteHTTPStatus(t, status, 201, raw)
	rec, err := noxmcp.LoadSuiteRegistrationRecord(raw)
	if err != nil {
		t.Fatal(err)
	}
	if rec.AssociationID != participant.Service.AssociationID {
		t.Fatal("registry lost paired identity")
	}
	path := "/api/nox/events?waitMs=0&after=" + url.QueryEscape(before.Cursor)
	status, _, raw = f.call(t, "GET", path, nil, gateway.ServiceCredential, nil)
	suiteHTTPStatus(t, status, 200, raw)
	reduced, err := noxmcp.ReduceSuiteRegistry(before, raw, before.Cursor)
	if err != nil || len(reduced.Products) != 1 {
		t.Fatal("HTTP feed cannot reconcile with SDK", err)
	}
	req := noxmcp.SuiteObservationEpochRequest{ContractVersion: "1.0", ProductID: rec.ProductID, RegistrationRevision: rec.Revision, AssociationID: rec.AssociationID, ProducerID: gateway.Service.ServiceID, Source: "gateway"}
	status, _, raw = f.call(t, "POST", "/api/nox/products/"+rec.ProductID+"/observation-epochs", req, gateway.ServiceCredential, nil)
	suiteHTTPStatus(t, status, 201, raw)
	epoch, err := noxmcp.LoadSuiteObservationEpoch(raw)
	if err != nil {
		t.Fatal(err)
	}
	obs := noxmcp.SuiteObservation{ContractVersion: "1.0", ProductID: rec.ProductID, RegistrationRevision: rec.Revision, AssociationID: rec.AssociationID, ProducerID: gateway.Service.ServiceID, Source: "gateway", ProducerEpoch: epoch.ProducerEpoch, Sequence: "18446744073709551615", ObservedAt: time.Now().UTC().Format(time.RFC3339Nano), State: "ready"}
	// The wire timestamp has millisecond precision at most.
	obs.ObservedAt = time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	status, _, raw = f.call(t, "POST", "/api/nox/products/"+rec.ProductID+"/observations", obs, gateway.ServiceCredential, nil)
	suiteHTTPStatus(t, status, 200, raw)
	if !bytes.Contains(raw, []byte(`"accepted":true`)) {
		t.Fatal("valid report ignored")
	}
	status, _, raw = f.call(t, "POST", "/api/nox/products/"+rec.ProductID+"/observations", obs, gateway.ServiceCredential, nil)
	suiteHTTPStatus(t, status, 200, raw)
	if !bytes.Contains(raw, []byte(`"accepted":false`)) {
		t.Fatal("duplicate report generated another event")
	}
	status, _, raw = f.call(t, "POST", "/api/nox/products/"+rec.ProductID+"/observations", obs, participant.ServiceCredential, nil)
	suiteHTTPStatus(t, status, 403, raw)
	for _, query := range []string{"after=bad", "waitMs=25001&after=bad", "limit=0&after=bad", "after=bad&after=other", "waitMs=1.5&after=bad"} {
		status, _, raw = f.call(t, "GET", "/api/nox/events?"+query, nil, gateway.ServiceCredential, nil)
		if status != 400 && status != 410 {
			t.Fatalf("unbounded query accepted: %d %s", status, raw)
		}
	}
	status, _, raw = f.call(t, "GET", path, nil, f.adminKey, nil)
	suiteHTTPStatus(t, status, 403, raw)
	status, _, raw = f.call(t, "GET", "/api/nox/products", nil, gateway.ServiceCredential, nil)
	suiteHTTPStatus(t, status, 200, raw)
	snapshot, err := noxmcp.LoadSuiteRegistrySnapshot(raw)
	if err != nil || len(snapshot.Observations) != 1 {
		t.Fatal("snapshot did not include report", err)
	}
	empty := "/api/nox/events?waitMs=20&after=" + url.QueryEscape(snapshot.Cursor)
	status, _, raw = f.call(t, "GET", empty, nil, gateway.ServiceCredential, nil)
	suiteHTTPStatus(t, status, 200, raw)
	page, err := noxmcp.LoadSuiteEventPage(raw)
	if err != nil || len(page.Events) != 0 || page.HasMore {
		t.Fatal("normal long-poll timeout was an error", err)
	}
}

func TestSuiteHTTPEventWaitUsesFullContractBudget(t *testing.T) {
	if testing.Short() {
		t.Skip("validates the complete 25-second contract wait")
	}
	f := suiteHTTPServer(t)
	gateway, _ := f.paired(t, "budget-router", "gateway")
	status, _, raw := f.call(t, "GET", "/api/nox/products", nil, gateway.ServiceCredential, nil)
	suiteHTTPStatus(t, status, 200, raw)
	snapshot, err := noxmcp.LoadSuiteRegistrySnapshot(raw)
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/nox/events?after=" + url.QueryEscape(snapshot.Cursor)
	req, err := http.NewRequest("GET", f.backend.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+gateway.ServiceCredential)
	done := make(chan error, 1)
	started := time.Now()
	go func() {
		response, err := f.client.Do(req)
		if err != nil {
			done <- err
			return
		}
		defer response.Body.Close()
		body, err := io.ReadAll(response.Body)
		if err == nil && response.StatusCode != 200 {
			err = fmt.Errorf("long poll returned %d: %s", response.StatusCode, body)
		}
		if err == nil {
			_, err = noxmcp.LoadSuiteEventPage(body)
		}
		done <- err
	}()
	end := time.Now().Add(3 * time.Second)
	for {
		f.s.suitePollMu.Lock()
		active := f.s.suitePolls[gateway.Service.ServiceID]
		f.s.suitePollMu.Unlock()
		if active {
			break
		}
		if time.Now().After(end) {
			t.Fatal("event request never started")
		}
		time.Sleep(5 * time.Millisecond)
	}
	status, _, raw = f.call(t, "GET", path+"&waitMs=0", nil, gateway.ServiceCredential, nil)
	suiteHTTPStatus(t, status, 503, raw)
	// A waiting subscription must leave the only SQLite connection available.
	if _, err = f.store.UpsertAllowedUser(context.Background(), "during-poll@example.com", "USER"); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
		elapsed := time.Since(started)
		if elapsed < 25*time.Second || elapsed > 29*time.Second {
			t.Fatalf("incorrect long-poll budget: %s", elapsed)
		}
	case <-time.After(29 * time.Second):
		t.Fatal("event wait exceeded its contract budget")
	}
}
