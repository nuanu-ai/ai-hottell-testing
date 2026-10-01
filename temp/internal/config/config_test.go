package config_test

import (
	"log/slog"
	"reflect"
	"strings"
	"testing"
	"time"

	"git.alva.dev/alva/harness-telemetry/internal/config"
)

func TestLoad(t *testing.T) {
	t.Parallel()

	const dbURL = "postgres://ht:ht@localhost:5432/ht?sslmode=disable"

	defaults := config.Config{
		HTTPAddr:          ":8080",
		DatabaseURL:       dbURL,
		LogLevel:          slog.LevelInfo,
		ShutdownTimeout:   15 * time.Second,
		PublicOrigin:      "http://localhost:8080",
		CookieSecure:      false,
		WebAuthnRPID:      "localhost",
		WebAuthnRPOrigins: []string{"http://localhost:8080", "http://localhost:5173"},
		CollectorURL:      "http://localhost:14318",
		GiteaURL:          "https://git.alva.dev",
		GiteaRepo:         "alva/harness-telemetry",
	}

	tests := []struct {
		name    string
		env     map[string]string
		want    config.Config
		wantErr string
	}{
		{
			name: "defaults",
			env:  map[string]string{"HT_DATABASE_URL": dbURL},
			want: defaults,
		},
		{
			name: "overrides",
			env: map[string]string{
				"HT_HTTP_ADDR":           "127.0.0.1:9090",
				"HT_DATABASE_URL":        dbURL,
				"HT_LOG_LEVEL":           "debug",
				"HT_SHUTDOWN_TIMEOUT":    "3s",
				"HT_PUBLIC_ORIGIN":       "HTTPS://Telemetry.Example.test:443/",
				"HT_COOKIE_SECURE":       "true",
				"HT_WEBAUTHN_RP_ID":      "telemetry.example.test",
				"HT_WEBAUTHN_RP_ORIGINS": " https://telemetry.example.test/ , https://alt.example.test:8443, http://localhost:80,",
				"HT_COLLECTOR_URL":       "http://collector:4318/",
				"HT_GITEA_URL":           "http://gitea.example.test:3000/",
				"HT_GITEA_REPO":          " team/hottell ",
				"HT_GITEA_TOKEN":         "gitea-token",
			},
			want: config.Config{
				HTTPAddr:          "127.0.0.1:9090",
				DatabaseURL:       dbURL,
				LogLevel:          slog.LevelDebug,
				ShutdownTimeout:   3 * time.Second,
				PublicOrigin:      "https://telemetry.example.test",
				CookieSecure:      true,
				WebAuthnRPID:      "telemetry.example.test",
				WebAuthnRPOrigins: []string{"https://telemetry.example.test", "https://alt.example.test:8443", "http://localhost"},
				CollectorURL:      "http://collector:4318",
				GiteaURL:          "http://gitea.example.test:3000",
				GiteaRepo:         "team/hottell",
				GiteaToken:        "gitea-token",
			},
		},
		{
			name:    "missing database url",
			env:     map[string]string{},
			wantErr: "HT_DATABASE_URL",
		},
		{
			name:    "empty database url",
			env:     map[string]string{"HT_DATABASE_URL": ""},
			wantErr: "HT_DATABASE_URL",
		},
		{
			name:    "invalid log level",
			env:     map[string]string{"HT_DATABASE_URL": dbURL, "HT_LOG_LEVEL": "verbose"},
			wantErr: "HT_LOG_LEVEL",
		},
		{
			name:    "invalid duration",
			env:     map[string]string{"HT_DATABASE_URL": dbURL, "HT_SHUTDOWN_TIMEOUT": "soon"},
			wantErr: "HT_SHUTDOWN_TIMEOUT",
		},
		{
			name:    "public origin without scheme",
			env:     map[string]string{"HT_DATABASE_URL": dbURL, "HT_PUBLIC_ORIGIN": "localhost:8080"},
			wantErr: "HT_PUBLIC_ORIGIN",
		},
		{
			name:    "public origin with path",
			env:     map[string]string{"HT_DATABASE_URL": dbURL, "HT_PUBLIC_ORIGIN": "http://localhost:8080/app"},
			wantErr: "HT_PUBLIC_ORIGIN",
		},
		{
			name:    "invalid cookie secure",
			env:     map[string]string{"HT_DATABASE_URL": dbURL, "HT_COOKIE_SECURE": "sometimes"},
			wantErr: "HT_COOKIE_SECURE",
		},
		{
			name:    "invalid webauthn origin",
			env:     map[string]string{"HT_DATABASE_URL": dbURL, "HT_WEBAUTHN_RP_ORIGINS": "http://localhost:8080,ftp://x"},
			wantErr: "HT_WEBAUTHN_RP_ORIGINS",
		},
		{
			name:    "webauthn origins only separators",
			env:     map[string]string{"HT_DATABASE_URL": dbURL, "HT_WEBAUTHN_RP_ORIGINS": " , "},
			wantErr: "HT_WEBAUTHN_RP_ORIGINS",
		},
		{
			name:    "collector url without scheme",
			env:     map[string]string{"HT_DATABASE_URL": dbURL, "HT_COLLECTOR_URL": "collector:4318"},
			wantErr: "HT_COLLECTOR_URL",
		},
		{
			name:    "collector url with query",
			env:     map[string]string{"HT_DATABASE_URL": dbURL, "HT_COLLECTOR_URL": "http://collector:4318?x=1"},
			wantErr: "HT_COLLECTOR_URL",
		},
		{
			name:    "gitea url without scheme",
			env:     map[string]string{"HT_DATABASE_URL": dbURL, "HT_GITEA_URL": "git.alva.dev"},
			wantErr: "HT_GITEA_URL",
		},
		{
			name:    "gitea repo without owner",
			env:     map[string]string{"HT_DATABASE_URL": dbURL, "HT_GITEA_REPO": "harness-telemetry"},
			wantErr: "HT_GITEA_REPO",
		},
		{
			name:    "gitea repo with extra segment",
			env:     map[string]string{"HT_DATABASE_URL": dbURL, "HT_GITEA_REPO": "alva/harness-telemetry/releases"},
			wantErr: "HT_GITEA_REPO",
		},
		{
			name:    "gitea repo with dot segment",
			env:     map[string]string{"HT_DATABASE_URL": dbURL, "HT_GITEA_REPO": "../admin"},
			wantErr: "HT_GITEA_REPO",
		},
		{
			name:    "non-positive duration",
			env:     map[string]string{"HT_DATABASE_URL": dbURL, "HT_SHUTDOWN_TIMEOUT": "0s"},
			wantErr: "HT_SHUTDOWN_TIMEOUT",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := config.Load(func(key string) (string, bool) {
				v, ok := tt.env[key]
				return v, ok
			})

			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("Load() error = %v, want error naming %s", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Load() unexpected error: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("Load() = %+v, want %+v", got, tt.want)
			}
		})
	}
}
