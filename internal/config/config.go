// Package config loads the service configuration from the environment.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
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
	// ReleaseDir contains the installer, client binaries and checksums bundled with this server.
	ReleaseDir string
	// ClickHouseURL is the clickhouse:// address of the ClickHouse database the telemetry is
	// read from; its user is expected to have read-only access.
	ClickHouseURL string
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
	envReleaseDir      = "HT_RELEASE_DIR"
	envClickHouseURL   = "HT_CLICKHOUSE_URL"
	// The *_FILE variables name files holding a secret, as Docker Swarm mounts them under
	// /run/secrets; the distroless image has no shell to move them into a variable.
	envDatabasePasswordFile   = "HT_DATABASE_PASSWORD_FILE"
	envClickHousePasswordFile = "HT_CLICKHOUSE_PASSWORD_FILE"

	defaultHTTPAddr        = ":8080"
	defaultLogLevel        = "info"
	defaultShutdownTimeout = "15s"
	defaultPublicOrigin    = "http://localhost:8080"
	defaultCookieSecure    = "false"
	defaultWebAuthnRPID    = "localhost"
	defaultWebAuthnOrigins = "http://localhost:8080,http://localhost:5173"
	defaultCollectorURL    = "http://localhost:14318"
	defaultReleaseDir      = "/opt/hottell/releases"
	// The read-only user of compose.yaml on its host port of the native protocol; local
	// development credentials, not a secret.
	defaultClickHouseURL = "clickhouse://ht_reader:ht_reader@localhost:19000/otel"
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
	if path := valueOr(lookup, envDatabasePasswordFile, ""); path != "" {
		withPassword, err := withPasswordFile(envDatabaseURL, envDatabasePasswordFile, databaseURL, path)
		if err != nil {
			return Config{}, err
		}
		cfg.DatabaseURL = withPassword
	}

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

	cfg.ReleaseDir = valueOr(lookup, envReleaseDir, defaultReleaseDir)

	clickHouseURL := valueOr(lookup, envClickHouseURL, defaultClickHouseURL)
	if err := checkClickHouseURL(clickHouseURL); err != nil {
		return Config{}, fmt.Errorf("%s: %w", envClickHouseURL, err)
	}
	if path := valueOr(lookup, envClickHousePasswordFile, ""); path != "" {
		// The default is the local address with its local password; a password file is for
		// an address given explicitly.
		if valueOr(lookup, envClickHouseURL, "") == "" {
			return Config{}, fmt.Errorf("%s: %w with %s", envClickHouseURL, errRequired, envClickHousePasswordFile)
		}
		withPassword, err := withPasswordFile(envClickHouseURL, envClickHousePasswordFile, clickHouseURL, path)
		if err != nil {
			return Config{}, err
		}
		clickHouseURL = withPassword
	}
	cfg.ClickHouseURL = clickHouseURL

	return cfg, nil
}

// checkClickHouseURL checks that s is a clickhouse://host[:port] URL of the native protocol.
// Errors never carry s: the URL holds the password.
func checkClickHouseURL(s string) error {
	u, err := url.Parse(s)
	if err != nil {
		// The *url.Error would quote the URL.
		return errors.New("not a URL")
	}
	if u.Scheme != "clickhouse" || u.Host == "" {
		return errors.New("want clickhouse://user:password@host:port/database")
	}
	return nil
}

// withPasswordFile returns rawURL, the value of the variable urlKey, with the password read from
// the file at path, named by the variable fileKey, put into its user info. Errors name the
// variables and never carry the URL or the password.
func withPasswordFile(urlKey, fileKey, rawURL, path string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		// The *url.Error would quote the URL, and a URL may carry a password.
		return "", fmt.Errorf("%s: not a URL, required with %s", urlKey, fileKey)
	}
	if u.User == nil || u.User.Username() == "" {
		return "", fmt.Errorf("%s: user required with %s", urlKey, fileKey)
	}
	// url.Query silently drops a parameter it cannot parse (an unescaped ";"), which a driver may
	// still read as the password, so only a query that parses whole is checked.
	query, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return "", fmt.Errorf("%s: malformed query, required to parse with %s", urlKey, fileKey)
	}
	// pgx and clickhouse-go both take a password query parameter over the user info password,
	// so either one is a password already in the URL.
	if _, set := u.User.Password(); set || query.Has("password") {
		return "", fmt.Errorf("%s: %s already carries a password: give it one way", fileKey, urlKey)
	}
	password, err := readSecretFile(fileKey, path)
	if err != nil {
		return "", err
	}
	u.User = url.UserPassword(u.User.Username(), password)
	return u.String(), nil
}

// readSecretFile returns the contents of the secret file at path, named by the variable key,
// without one trailing "\n" or "\r\n"; nothing else is trimmed. An unreadable or empty file is an
// error naming key; the error carries the path, never the contents.
func readSecretFile(key, path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("%s: %w", key, err)
	}
	secret := string(data)
	if s, ok := strings.CutSuffix(secret, "\r\n"); ok {
		secret = s
	} else {
		secret = strings.TrimSuffix(secret, "\n")
	}
	if secret == "" {
		return "", fmt.Errorf("%s: file %s is empty", key, path)
	}
	return secret, nil
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
