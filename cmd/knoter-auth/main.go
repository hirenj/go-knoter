// knoter-auth authenticates with Microsoft and prints an access token as an
// environment variable assignment, ready to be consumed by knoter.
//
// Usage:
//
//	eval "$(knoter-auth --login-hint you@company.com)"
//	knoter upload --notebook "Lab Notes" --section "2024" report.html
//
// The token is cached in ~/.config/knoter/token.json and reused on subsequent
// runs until it expires.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/hirenj/go-knoter/internal/auth"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "knoter-auth:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	fs := flag.NewFlagSet("knoter-auth", flag.ContinueOnError)

	clientID     := fs.String("client-id", auth.DefaultClientID, "Azure app client ID")
	clientSecret := fs.String("client-secret", "", "Azure app client secret (confidential client apps only)")
	tenant       := fs.String("tenant", auth.DefaultTenant, `Azure AD tenant: "common", "consumers", "organizations", or a tenant ID`)
	loginHint    := fs.String("login-hint", "", "Your Microsoft account email; derives tenant and pre-fills sign-in")
	flow         := fs.String("flow", auth.FlowDeviceCode, `Auth flow: "device-code" or "pkce"`)
	sharepoint   := fs.String("sharepoint", "", "SharePoint site URL — requests Sites.Read.All scope")
	envVar       := fs.String("env-var", "KNOTER_TOKEN", "Name of the environment variable to output")
	logoutFlag   := fs.Bool("logout", false, "Clear the cached token and exit")

	if err := fs.Parse(args); err != nil {
		return err
	}

	if *logoutFlag {
		if err := auth.Logout(); err != nil && !os.IsNotExist(err) {
			return err
		}
		fmt.Fprintln(os.Stderr, "Logged out (token cache cleared).")
		return nil
	}

	// Derive tenant from --login-hint or --sharepoint when not explicitly set.
	if *tenant == auth.DefaultTenant {
		if *loginHint != "" {
			if i := strings.LastIndex(*loginHint, "@"); i >= 0 {
				*tenant = (*loginHint)[i+1:]
			}
		} else if *sharepoint != "" {
			if t := tenantFromSharePoint(*sharepoint); t != "" {
				*tenant = t
			}
		}
	}

	scope := auth.DefaultScope
	if *sharepoint != "" {
		scope = auth.SharePointScope
	}

	token, err := auth.GetToken(context.Background(), *clientID, *clientSecret, *tenant, *loginHint, *flow, scope)
	if err != nil {
		return fmt.Errorf("authentication: %w", err)
	}

	// Print as a shell assignment to stdout so the caller can eval it.
	fmt.Printf("%s=%s\n", *envVar, token.AccessToken)
	return nil
}

// tenantFromSharePoint mirrors the logic in cmd/knoter.
func tenantFromSharePoint(rawURL string) string {
	host := rawURL
	if i := strings.Index(host, "://"); i >= 0 {
		host = host[i+3:]
	}
	if i := strings.Index(host, "/"); i >= 0 {
		host = host[:i]
	}
	const suffix = ".sharepoint.com"
	if strings.HasSuffix(strings.ToLower(host), suffix) {
		return host[:len(host)-len(suffix)] + ".onmicrosoft.com"
	}
	return ""
}
