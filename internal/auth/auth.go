// Package auth handles Microsoft OAuth2 authentication for the OneNote API.
// It supports two flows:
//   - Device code (FlowDeviceCode): no browser on the machine needed; user
//     visits a URL on any device.  Some tenants block this via Conditional
//     Access policies.
//   - Auth code + PKCE (FlowPKCE): opens the local browser and starts a
//     temporary loopback HTTP server to capture the redirect.  Works even
//     when device code is blocked.
//
// Tokens are cached in ~/.config/knoter/token.json so the user only logs in
// once.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"golang.org/x/term"
)

// GraphAudience is the audience value required by the Microsoft Graph v2 API.
const GraphAudience = "https://graph.microsoft.com"

// CheckTokenAudience decodes the JWT payload and returns an error if the
// token's audience is not GraphAudience.  Pass the raw access token string.
func CheckTokenAudience(accessToken string) error {
	parts := strings.Split(accessToken, ".")
	if len(parts) < 2 {
		return nil // not a JWT; let the API surface any error
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil // can't decode; let the API surface any error
	}
	var claims struct {
		Aud interface{} `json:"aud"` // string or []string
		Ver string      `json:"ver"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil
	}
	var audiences []string
	switch v := claims.Aud.(type) {
	case string:
		audiences = []string{v}
	case []interface{}:
		for _, a := range v {
			if s, ok := a.(string); ok {
				audiences = append(audiences, s)
			}
		}
	}
	for _, a := range audiences {
		if a == GraphAudience {
			return nil
		}
	}
	if len(audiences) > 0 {
		hint := ""
		if claims.Ver == "1.0" {
			hint = "\nThis looks like an Azure AD v1 token (ver=1.0). " +
				"Microsoft Graph requires a v2 token. " +
				"Re-authenticate using knoter's own auth flow (omit --token-env) " +
				"to obtain a token targeting https://graph.microsoft.com."
		}
		return fmt.Errorf("token audience is %q but Microsoft Graph requires %q%s",
			strings.Join(audiences, ", "), GraphAudience, hint)
	}
	return nil
}

const (
	// Public client ID used by knoter (matches the original R package registration).
	// Users may override via --client-id flag.
	DefaultClientID = "a11d0a44-5bd4-44e1-b879-f3a76a56c84a"
	// DefaultTenant is the Azure AD tenant used for auth.
	// Use "common" for multi-tenant apps, "consumers" for personal Microsoft
	// accounts, "organizations" for work/school accounts, or a specific tenant ID.
	DefaultTenant = "common"

	// FlowDeviceCode uses the OAuth2 device authorization grant (RFC 8628).
	FlowDeviceCode = "device-code"
	// FlowPKCE uses authorization code + PKCE with a local loopback server.
	FlowPKCE = "pkce"

	// DefaultScope is the OAuth scope used for personal OneNote.
	DefaultScope = "Notes.ReadWrite.All offline_access"
	// SharePointScope adds site-resolution permission for SharePoint notebooks.
	SharePointScope = "Notes.ReadWrite.All Sites.Read.All offline_access"
)

var httpClient = &http.Client{Timeout: 30 * time.Second}

func authEndpoint(tenant string) string {
	return "https://login.microsoftonline.com/" + tenant + "/oauth2/v2.0/authorize"
}

func tokenEndpoint(tenant string) string {
	return "https://login.microsoftonline.com/" + tenant + "/oauth2/v2.0/token"
}

func deviceEndpoint(tenant string) string {
	return "https://login.microsoftonline.com/" + tenant + "/oauth2/v2.0/devicecode"
}

// Token mirrors the fields we need from a Microsoft token response.
type Token struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	TokenType    string    `json:"token_type"`
	ExpiresAt    time.Time `json:"expires_at"`
}

func (t *Token) Valid() bool {
	return t != nil && t.AccessToken != "" && time.Now().Before(t.ExpiresAt.Add(-30*time.Second))
}

// CachePath returns the OS-appropriate token cache path.
func CachePath() string {
	cfgDir, err := os.UserConfigDir()
	if err != nil {
		cfgDir = os.Getenv("HOME")
	}
	return filepath.Join(cfgDir, "knoter", "token.json")
}

// Load reads a cached token from disk. Returns nil if absent or unreadable.
func Load() *Token {
	data, err := os.ReadFile(CachePath())
	if err != nil {
		return nil
	}
	var t Token
	if err := json.Unmarshal(data, &t); err != nil {
		return nil
	}
	return &t
}

// Save persists the token to disk with 0600 permissions.
func Save(t *Token) error {
	path := CachePath()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	data, err := json.Marshal(t)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}

// Refresh uses the cached refresh token to obtain a new access token.
func Refresh(clientID, clientSecret, tenant, scope string, t *Token) (*Token, error) {
	vals := url.Values{
		"client_id":     {clientID},
		"grant_type":    {"refresh_token"},
		"refresh_token": {t.RefreshToken},
		"scope":         {scope},
	}
	if clientSecret != "" {
		vals.Set("client_secret", clientSecret)
	}
	return postToken(tenant, vals)
}

// GetToken returns a valid access token, refreshing or prompting as needed.
// flow should be FlowDeviceCode or FlowPKCE.
// clientSecret is optional; required only for confidential client app registrations.
// loginHint is optional; when set it pre-fills the sign-in page.
func GetToken(ctx context.Context, clientID, clientSecret, tenant, loginHint, flow, scope string) (*Token, error) {
	t := Load()

	if t != nil && t.Valid() {
		return t, nil
	}

	if t != nil && t.RefreshToken != "" {
		refreshed, err := Refresh(clientID, clientSecret, tenant, scope, t)
		if err == nil {
			_ = Save(refreshed)
			return refreshed, nil
		}
		// Fall through to interactive login if refresh fails.
	}

	var (
		fresh *Token
		err   error
	)
	switch flow {
	case FlowPKCE:
		fresh, err = AuthCodePKCEFlow(ctx, clientID, clientSecret, tenant, loginHint, scope)
	default:
		fresh, err = DeviceCodeFlow(ctx, clientID, tenant, loginHint, scope)
	}
	if err != nil {
		return nil, err
	}
	_ = Save(fresh)
	return fresh, nil
}

// Logout deletes the cached token.
func Logout() error {
	return os.Remove(CachePath())
}

// ---- Device code flow ------------------------------------------------------

// DeviceCodeFlow runs the interactive device-code login and returns a token.
// loginHint is optional; when set (e.g. "user@contoso.com") it pre-fills the
// sign-in page and must be consistent with the tenant.
func DeviceCodeFlow(ctx context.Context, clientID, tenant, loginHint, scope string) (*Token, error) {
	// Step 1: request device code
	params := url.Values{
		"client_id": {clientID},
		"scope":     {scope},
	}
	if loginHint != "" {
		params.Set("login_hint", loginHint)
	}
	resp, err := httpClient.PostForm(deviceEndpoint(tenant), params)
	if err != nil {
		return nil, fmt.Errorf("device code request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading device code response: %w", err)
	}

	var dc struct {
		DeviceCode      string `json:"device_code"`
		UserCode        string `json:"user_code"`
		VerificationURI string `json:"verification_uri"`
		Message         string `json:"message"`
		Interval        int    `json:"interval"`
		ExpiresIn       int    `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &dc); err != nil {
		return nil, fmt.Errorf("decode device code response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		var apiErr struct {
			Error       string `json:"error"`
			Description string `json:"error_description"`
		}
		_ = json.Unmarshal(body, &apiErr)
		if apiErr.Error != "" {
			return nil, fmt.Errorf("device code request: %s: %s", apiErr.Error, apiErr.Description)
		}
		return nil, fmt.Errorf("device code request: HTTP %d", resp.StatusCode)
	}

	// Print the message (contains URL + code) to stderr so stdout stays clean.
	fmt.Fprintln(os.Stderr, "\n"+dc.Message+"\n")

	// Step 2: poll for token
	interval := time.Duration(dc.Interval) * time.Second
	if interval == 0 {
		interval = 5 * time.Second
	}
	deadline := time.Now().Add(time.Duration(dc.ExpiresIn) * time.Second)

	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(interval):
		}

		tok, err := postToken(tenant, url.Values{
			"client_id":   {clientID},
			"grant_type":  {"urn:ietf:params:oauth2:grant-type:device_code"},
			"device_code": {dc.DeviceCode},
		})
		if err != nil {
			if strings.Contains(err.Error(), "authorization_pending") {
				continue
			}
			return nil, err
		}
		return tok, nil
	}
	return nil, fmt.Errorf("device code flow timed out")
}

// ---- Auth code + PKCE flow -------------------------------------------------

// AuthCodePKCEFlow opens the browser for sign-in. After authenticating, Azure
// redirects to Microsoft's native-client URI with the auth code in the URL.
// The user copies that URL from the browser's address bar and pastes it into
// the terminal; knoter parses the code from it.
//
// Using the native-client redirect URI avoids the need for a client secret
// (unlike web-platform redirect URIs) and requires no local HTTP server.
func AuthCodePKCEFlow(ctx context.Context, clientID, clientSecret, tenant, loginHint, scope string) (*Token, error) {
	verifier, err := generateCodeVerifier()
	if err != nil {
		return nil, fmt.Errorf("generating PKCE verifier: %w", err)
	}
	state, err := randomBase64(16)
	if err != nil {
		return nil, fmt.Errorf("generating state: %w", err)
	}

	// Microsoft's built-in native/public-client redirect URI.  It is accepted
	// by any app with allowPublicClient=true and requires no client secret.
	const redirectURI = "https://login.microsoftonline.com/common/oauth2/nativeclient"

	params := url.Values{
		"client_id":             {clientID},
		"response_type":         {"code"},
		"redirect_uri":          {redirectURI},
		"scope":                 {scope},
		"code_challenge":        {codeChallenge(verifier)},
		"code_challenge_method": {"S256"},
		"state":                 {state},
	}
	if loginHint != "" {
		params.Set("login_hint", loginHint)
	}
	authURL := authEndpoint(tenant) + "?" + params.Encode()

	fmt.Fprintf(os.Stderr, "\nSign in with this URL:\n%s\n\n", authURL)
	if err := openBrowser(authURL); err == nil {
		fmt.Fprintf(os.Stderr, "(Browser opened automatically.)\n\n")
	}
	fmt.Fprintf(os.Stderr, "After signing in, your browser will land on a blank page.\n")
	fmt.Fprintf(os.Stderr, "Copy the full URL from the address bar and paste it here: ")

	tty, err := os.Open("/dev/tty")
	if err != nil {
		tty = os.Stdin
	} else {
		defer tty.Close()
	}
	raw, err := readLine(tty)
	if err != nil {
		return nil, fmt.Errorf("reading URL: %w", err)
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("no URL entered")
	}

	parsed, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("parsing redirect URL: %w", err)
	}
	q := parsed.Query()
	if errCode := q.Get("error"); errCode != "" {
		return nil, fmt.Errorf("auth error: %s: %s", errCode, q.Get("error_description"))
	}
	if q.Get("state") != state {
		return nil, fmt.Errorf("state mismatch — possible CSRF; please try again")
	}
	code := q.Get("code")
	if code == "" {
		return nil, fmt.Errorf("no code found in URL")
	}

	vals := url.Values{
		"client_id":     {clientID},
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {redirectURI},
		"code_verifier": {verifier},
	}
	if clientSecret != "" {
		vals.Set("client_secret", clientSecret)
	}
	return postToken(tenant, vals)
}

// ---- Terminal input --------------------------------------------------------

// readLine reads one line from f in raw mode, bypassing the terminal's
// MAX_CANON limit (1024 bytes on macOS) which would otherwise truncate long
// auth codes.  It strips ANSI/VT escape sequences (e.g. bracketed-paste
// markers ESC[200~…ESC[201~) and treats \r and \n as line terminators.
func readLine(f *os.File) (string, error) {
	fd := int(f.Fd())
	if old, err := term.MakeRaw(fd); err == nil {
		defer term.Restore(fd, old)
	}

	var code strings.Builder
	buf := make([]byte, 4096)
	inEsc := false
	inCsi := false

	for {
		n, err := f.Read(buf)
		for _, b := range buf[:n] {
			switch {
			case inEsc:
				if b == '[' {
					inCsi = true
				}
				inEsc = false
			case inCsi:
				if b >= 0x40 && b <= 0x7e {
					inCsi = false
				}
			case b == 0x1b:
				inEsc = true
			case b == '\r' || b == '\n':
				if code.Len() > 0 {
					return code.String(), nil
				}
			case b >= 0x20 && b <= 0x7e:
				code.WriteByte(b)
			}
		}
		if err == io.EOF {
			return code.String(), nil
		}
		if err != nil {
			return "", err
		}
	}
}

// ---- PKCE helpers ----------------------------------------------------------

func generateCodeVerifier() (string, error) {
	return randomBase64(32)
}

func codeChallenge(verifier string) string {
	h := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(h[:])
}

func randomBase64(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func openBrowser(u string) error {
	var cmd string
	var args []string
	switch runtime.GOOS {
	case "windows":
		// "start" needs an empty title arg when the URL contains special chars.
		cmd = "cmd"
		args = []string{"/c", "start", "", u}
	case "darwin":
		cmd = "open"
		args = []string{u}
	default:
		cmd = "xdg-open"
		args = []string{u}
	}
	return exec.Command(cmd, args...).Start()
}

// ---- Token endpoint --------------------------------------------------------

// postToken is the shared token-endpoint POST helper.
func postToken(tenant string, vals url.Values) (*Token, error) {
	resp, err := httpClient.PostForm(tokenEndpoint(tenant), vals)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var raw map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, err
	}

	if errCode, ok := raw["error"].(string); ok {
		desc, _ := raw["error_description"].(string)
		return nil, fmt.Errorf("%s: %s", errCode, desc)
	}

	accessToken, _ := raw["access_token"].(string)
	refreshToken, _ := raw["refresh_token"].(string)
	expiresIn, _ := raw["expires_in"].(float64)

	return &Token{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		TokenType:    "Bearer",
		ExpiresAt:    time.Now().Add(time.Duration(expiresIn) * time.Second),
	}, nil
}
