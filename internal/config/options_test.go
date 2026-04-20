package config

import "testing"

func resetOptions() {
	Options.AppPort = ""
	Options.CertFile = ""
	Options.KeyFile = ""
	Options.DatabaseDSN = ""
	Options.MinioEndpoint = ""
	Options.MinioAccessKey = ""
	Options.MinioSecretKey = ""
	Options.MinioBucket = ""
	Options.MinioUseSSL = false
}

func TestApplyEnvOverrides_AllSet(t *testing.T) {
	resetOptions()
	env := map[string]string{
		"SERVER_ADDRESS":   ":9090",
		"DATABASE_DSN":     "postgres://u:p@host/db",
		"SERVER_CERT":      "/etc/tls/cert.pem",
		"SERVER_KEY":       "/etc/tls/key.pem",
		"MINIO_ENDPOINT":   "minio:9000",
		"MINIO_ACCESS_KEY": "access",
		"MINIO_SECRET_KEY": "secret",
		"MINIO_BUCKET":     "avatars",
	}
	applyEnvOverrides(func(k string) (string, bool) {
		v, ok := env[k]
		return v, ok
	})

	want := map[string]string{
		"AppPort":        ":9090",
		"DatabaseDSN":    "postgres://u:p@host/db",
		"CertFile":       "/etc/tls/cert.pem",
		"KeyFile":        "/etc/tls/key.pem",
		"MinioEndpoint":  "minio:9000",
		"MinioAccessKey": "access",
		"MinioSecretKey": "secret",
		"MinioBucket":    "avatars",
	}
	got := map[string]string{
		"AppPort":        Options.AppPort,
		"DatabaseDSN":    Options.DatabaseDSN,
		"CertFile":       Options.CertFile,
		"KeyFile":        Options.KeyFile,
		"MinioEndpoint":  Options.MinioEndpoint,
		"MinioAccessKey": Options.MinioAccessKey,
		"MinioSecretKey": Options.MinioSecretKey,
		"MinioBucket":    Options.MinioBucket,
	}
	for k, w := range want {
		if got[k] != w {
			t.Errorf("%s: got %q, want %q", k, got[k], w)
		}
	}
}

func TestApplyEnvOverrides_PreservesWhenUnset(t *testing.T) {
	resetOptions()
	Options.AppPort = "localhost:8080"
	Options.DatabaseDSN = "flag-dsn"

	applyEnvOverrides(func(string) (string, bool) { return "", false })

	if Options.AppPort != "localhost:8080" {
		t.Errorf("AppPort was overwritten: got %q", Options.AppPort)
	}
	if Options.DatabaseDSN != "flag-dsn" {
		t.Errorf("DatabaseDSN was overwritten: got %q", Options.DatabaseDSN)
	}
}

func TestApplyEnvOverrides_EmptyStringIsAnOverride(t *testing.T) {
	resetOptions()
	Options.DatabaseDSN = "flag-dsn"

	applyEnvOverrides(func(k string) (string, bool) {
		if k == "DATABASE_DSN" {
			return "", true
		}
		return "", false
	})

	if Options.DatabaseDSN != "" {
		t.Errorf("empty env var did not override: got %q", Options.DatabaseDSN)
	}
}

func TestApplyEnvOverrides_PartialOverride(t *testing.T) {
	resetOptions()
	Options.AppPort = "localhost:8080"
	Options.DatabaseDSN = "flag-dsn"

	env := map[string]string{"DATABASE_DSN": "env-dsn"}
	applyEnvOverrides(func(k string) (string, bool) {
		v, ok := env[k]
		return v, ok
	})

	if Options.AppPort != "localhost:8080" {
		t.Errorf("AppPort should be untouched: got %q", Options.AppPort)
	}
	if Options.DatabaseDSN != "env-dsn" {
		t.Errorf("DatabaseDSN should be overridden: got %q", Options.DatabaseDSN)
	}
}
