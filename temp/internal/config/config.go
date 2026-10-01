// Package config loads the service configuration from the environment.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Config is the typed service configuration.
type Config struct {
	HTTPAddr        string
	DatabaseURL     string
	LogLevel        slog.Level
	ShutdownTimeout time.Duration
	// PublicOrigin is the origin the service is opened at, without a trailing "/".
	PublicOrigin string
	// CookieSecure marks the cookies the service sets as Secure.
	CookieSecure bool
	// WebAuthnRPID is the WebAuthn relying party ID.
	WebAuthnRPID string
	// WebAuthnRPOrigins are the origins WebAuthn ceremonies may come from.
	WebAuthnRPOrigins []string
	// CollectorURL is the base OTLP/HTTP address of the collector, without a trailing "/".
	CollectorURL string
	// GiteaURL is the base address of the Gitea the hottell releases are taken from, without
	// a trailing "/".
	GiteaURL string
	// GiteaRepo is the owner/name of the Gitea repository holding the hottell releases.
	GiteaRepo string
	// GiteaToken is the Gitea access token the releases are read with; empty reads them
	// anonymously.
	GiteaToken string
}

// LookupFunc reports the value of an environment variable and whether it is set.
type LookupFunc func(key string) (string, bool)

const (
	envHTTPAddr        = "HT_HTTP_ADDR"
	envDatabaseURL     = "HT_DATABASE_URL"
	envLogLevel        = "HT_LOG_LEVEL"
	envShutdownTimeout = "HT_SHUTDOWN_TIMEOUT"
	envPublicOrigin    = "HT_PUBLIC_ORIGIN"
	envCookieSecure    = "HT_COOKIE_SECURE"
	envWebAuthnRPID    = "HT_WEBAUTHN_RP_ID"
	envWebAuthnOrigins = "HT_WEBAUTHN_RP_ORIGINS"
	envCollectorURL    = "HT_COLLECTOR_URL"
	envGiteaURL        = "HT_GITEA_URL"
	envGiteaRepo       = "HT_GITEA_REPO"
	envGiteaToken      = "HT_GITEA_TOKEN"

	defaultHTTPAddr        = ":8080"
	defaultLogLevel        = "info"
	defaultShutdownTimeout = "15s"
	defaultPublicOrigin    = "http://localhost:8080"
	defaultCookieSecure    = "false"
	defaultWebAuthnRPID    = "localhost"
	defaultWebAuthnOrigins = "http://localhost:8080,http://localhost:5173"
	defaultCollectorURL    = "http://localhost:14318"
	defaultGiteaURL        = "https://git.alva.dev"
	defaultGiteaRepo       = "alva/harness-telemetry"
)

var errRequired = errors.New("required but not set")

// Load reads the configuration through lookup and validates it.
// An invalid value fails with an error naming its variable.
func Load(lookup LookupFunc) (Config, error) {
	cfg := Config{HTTPAddr: valueOr(lookup, envHTTPAddr, defaultHTTPAddr)}

	databaseURL, ok := lookup(envDatabaseURL)
	if !ok || databaseURL == "" {
		return Config{}, fmt.Errorf("%s: %w", envDatabaseURL, errRequired)
	}
	cfg.DatabaseURL = databaseURL

	level, err := parseLogLevel(valueOr(lookup, envLogLevel, defaultLogLevel))
	if err != nil {
		return Config{}, fmt.Errorf("%s: %w", envLogLevel, err)
	}
	cfg.LogLevel = level

	timeout, err := parsePositiveDuration(valueOr(lookup, envShutdownTimeout, defaultShutdownTimeout))
	if err != nil {
		return Config{}, fmt.Errorf("%s: %w", envShutdownTimeout, err)
	}
	cfg.ShutdownTimeout = timeout

	origin, err := parseOrigin(valueOr(lookup, envPublicOrigin, defaultPublicOrigin))
	if err != nil {
		return Config{}, fmt.Errorf("%s: %w", envPublicOrigin, err)
	}
	cfg.PublicOrigin = origin

	secure, err := strconv.ParseBool(valueOr(lookup, envCookieSecure, defaultCookieSecure))
	if err != nil {
		return Config{}, fmt.Errorf("%s: %w", envCookieSecure, err)
	}
	cfg.CookieSecure = secure

	cfg.WebAuthnRPID = valueOr(lookup, envWebAuthnRPID, defaultWebAuthnRPID)

	origins, err := parseOrigins(valueOr(lookup, envWebAuthnOrigins, defaultWebAuthnOrigins))
	if err != nil {
		return Config{}, fmt.Errorf("%s: %w", envWebAuthnOrigins, err)
	}
	cfg.WebAuthnRPOrigins = origins

	collectorURL, err := parseBaseURL(valueOr(lookup, envCollectorURL, defaultCollectorURL))
	if err != nil {
		return Config{}, fmt.Errorf("%s: %w", envCollectorURL, err)
	}
	cfg.CollectorURL = collectorURL

	giteaURL, err := parseBaseURL(valueOr(lookup, envGiteaURL, defaultGiteaURL))
	if err != nil {
		return Config{}, fmt.Errorf("%s: %w", envGiteaURL, err)
	}
	cfg.GiteaURL = giteaURL

	giteaRepo, err := parseRepo(valueOr(lookup, envGiteaRepo, defaultGiteaRepo))
	if err != nil {
		return Config{}, fmt.Errorf("%s: %w", envGiteaRepo, err)
	}
	cfg.GiteaRepo = giteaRepo

	cfg.GiteaToken, _ = lookup(envGiteaToken)

	return cfg, nil
}

func valueOr(lookup LookupFunc, key, fallback string) string {
	if v, ok := lookup(key); ok && v != "" {
		return v
	}
	return fallback
}

func parseLogLevel(s string) (slog.Level, error) {
	switch strings.ToLower(s) {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("invalid log level %q: want debug, info, warn or error", s)
	}
}

func parsePositiveDuration(s string) (time.Duration, error) {
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("invalid duration %q: %w", s, err)
	}
	if d <= 0 {
		return 0, fmt.Errorf("invalid duration %q: must be positive", s)
	}
	return d, nil
}

// parseOrigin returns s as a scheme://host[:port] origin, a trailing "/" dropped.
func parseOrigin(s string) (string, error) {
	origin := strings.TrimRight(strings.TrimSpace(s), "/")
	u, err := url.Parse(origin)
	if err != nil {
		return "", fmt.Errorf("invalid origin %q: %w", s, err)
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil ||
		u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("invalid origin %q: want http(s)://host[:port]", s)
	}
	// Serialized as a browser sends it in the Origin header: lower case, no default port.
	host := strings.ToLower(u.Host)
	host = strings.TrimSuffix(host, map[string]string{"http": ":80", "https": ":443"}[u.Scheme])
	return u.Scheme + "://" + host, nil
}

// parseBaseURL returns s as an http(s)://host[:port][/path] URL, a trailing "/" dropped.
func parseBaseURL(s string) (string, error) {
	base := strings.TrimRight(strings.TrimSpace(s), "/")
	u, err := url.Parse(base)
	if err != nil {
		return "", fmt.Errorf("invalid URL %q: %w", s, err)
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil ||
		u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return "", fmt.Errorf("invalid URL %q: want http(s)://host[:port][/path]", s)
	}
	return base, nil
}

// repoPattern is a Gitea owner/name: each part safe to put into a URL path as it is.
var repoPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

// parseRepo returns s as a Gitea owner/name.
func parseRepo(s string) (string, error) {
	repo := strings.TrimSpace(s)
	owner, name, _ := strings.Cut(repo, "/")
	if !repoPattern.MatchString(repo) || strings.Trim(owner, ".") == "" || strings.Trim(name, ".") == "" {
		return "", fmt.Errorf("invalid repository %q: want owner/name", s)
	}
	return repo, nil
}

// parseOrigins returns the comma-separated origins of s; at least one is required.
func parseOrigins(s string) ([]string, error) {
	var origins []string
	for part := range strings.SplitSeq(s, ",") {
		if strings.TrimSpace(part) == "" {
			continue
		}
		origin, err := parseOrigin(part)
		if err != nil {
			return nil, err
		}
		origins = append(origins, origin)
	}
	if len(origins) == 0 {
		return nil, errRequired
	}
	return origins, nil
}
