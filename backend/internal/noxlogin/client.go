package noxlogin

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	noxauth "github.com/mapherez/nox-auth/client"
	"github.com/mapherez/nox-backend/backend/internal/storage"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const CallbackPath = "/auth/nox/callback"

func Client(config storage.NoXLoginConfig) (*noxauth.Client, error) {
	return noxauth.New(noxauth.Config{Issuer: config.Issuer, ClientID: config.ClientID, ClientSecret: config.Secret, SocketPath: config.SocketPath})
}

// Admission is independent of browser session/logout. The caller first verifies
// its own credential and user role/status; this result never creates a login.
func Admission(ctx context.Context, config storage.NoXLoginConfig, email, googleSubject string) (time.Time, error) {
	started := time.Now()
	client, err := noxauth.HTTPClient(config.Issuer, config.SocketPath)
	if err != nil {
		return time.Time{}, err
	}
	body, _ := json.Marshal(map[string]string{"email": email, "google_sub": googleSubject})
	request, err := http.NewRequestWithContext(ctx, "POST", config.Issuer+"/v1/admission", bytes.NewReader(body))
	if err != nil {
		return time.Time{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.SetBasicAuth(config.ClientID, config.Secret)
	response, err := client.Do(request)
	if err != nil {
		return time.Time{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return time.Time{}, errors.New("NoX Auth account admission unavailable; update Auth to the service release with /v1/admission")
	}
	var result struct {
		Allowed bool `json:"allowed"`
	}
	if err = json.NewDecoder(io.LimitReader(response.Body, 4096)).Decode(&result); err != nil {
		return time.Time{}, err
	}
	if !result.Allowed {
		return time.Time{}, noxauth.ErrDenied
	}
	until := started.Add(noxauth.RevalidationInterval)
	if !time.Now().Before(until) {
		return time.Time{}, errors.New("Auth admission expired during validation")
	}
	return until, nil
}
func Setup(ctx context.Context, data *storage.Store, issuer, origin, adminSocket, runtimeSocket string) error {
	issuer = strings.TrimRight(strings.TrimSpace(issuer), "/")
	origin = strings.TrimRight(strings.TrimSpace(origin), "/")
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.ForceQuery || strings.Contains(origin, "#") {
		return errors.New("NOX_BACKEND_PUBLIC_URL must be the exact browser-facing HTTP(S) origin")
	}
	previous, found, err := data.NoXLoginConfiguration(ctx)
	if err != nil {
		return err
	}
	if found && previous.Issuer != issuer {
		return errors.New("NOX_AUTH_PUBLIC_URL differs from the enrolled issuer; the stored identity cannot be silently replaced")
	}
	if !found {
		previous.ClientID = "nox-backend-" + strings.ToLower(rand.Text())
		previous.Secret = noxauth.GenerateSecret()
	}
	config := storage.NoXLoginConfig{Issuer: issuer, Origin: origin, ClientID: previous.ClientID, Secret: previous.Secret, SocketPath: runtimeSocket}
	if _, err = Client(config); err != nil {
		return err
	}
	if err = data.SaveNoXLoginConfiguration(ctx, config); err != nil {
		return err
	}
	for {
		attempt, cancel := context.WithTimeout(ctx, 10*time.Second)
		err = noxauth.Register(attempt, adminSocket, config.ClientID, []string{origin + CallbackPath}, config.Secret, true)
		cancel()
		if err == nil {
			// Require the admission API before replacing an installed Backend.
			_, err = Admission(ctx, config, "setup-check@example.invalid", "")
			if errors.Is(err, noxauth.ErrDenied) {
				return nil
			}
			if err == nil {
				return nil
			}
			return err
		}
		select {
		case <-ctx.Done():
			return errors.New("Auth enrollment unavailable; run Auth first with the same shared local volumes")
		case <-time.After(time.Second):
		}
	}
}
