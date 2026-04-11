package config

import (
	"flag"
	"os"
)

var Options struct {
	AppPort        string `default:"localhost:8080"`
	CertFile       string
	KeyFile        string
	DatabaseDSN    string
	MinioEndpoint  string
	MinioAccessKey string
	MinioSecretKey string
	MinioBucket    string
	MinioUseSSL    bool
}

func ParseFlags() {
	flag.StringVar(&Options.AppPort, "a", "localhost:8080", "The address to bind the app to")
	flag.StringVar(&Options.CertFile, "c", "", "The TLS certificate file")
	flag.StringVar(&Options.KeyFile, "k", "", "The TLS key file")
	flag.StringVar(&Options.DatabaseDSN, "d", "", "Database connection string")
	flag.StringVar(&Options.MinioEndpoint, "minio-endpoint", "localhost:9002", "MinIO endpoint")
	flag.StringVar(&Options.MinioAccessKey, "minio-access-key", "minio_user", "MinIO access key")
	flag.StringVar(&Options.MinioSecretKey, "minio-secret-key", "minio_password", "MinIO secret key")
	flag.StringVar(&Options.MinioBucket, "minio-bucket", "gkeeper-secrets", "MinIO bucket name")
	flag.BoolVar(&Options.MinioUseSSL, "minio-use-ssl", false, "Use SSL for MinIO connection")
	flag.Parse()

	applyEnvOverrides(os.LookupEnv)
}

// lookupFunc matches os.LookupEnv so tests can inject a fake env.
type lookupFunc func(string) (string, bool)

// applyEnvOverrides overrides Options fields with environment values
// (or whatever the lookup function returns) when they are set.
// Split from ParseFlags so it can be unit-tested without touching
// the global flag.CommandLine.
func applyEnvOverrides(lookup lookupFunc) {
	overrides := []struct {
		env   string
		apply func(string)
	}{
		{"SERVER_ADDRESS", func(v string) { Options.AppPort = v }},
		{"DATABASE_DSN", func(v string) { Options.DatabaseDSN = v }},
		{"SERVER_CERT", func(v string) { Options.CertFile = v }},
		{"SERVER_KEY", func(v string) { Options.KeyFile = v }},
		{"MINIO_ENDPOINT", func(v string) { Options.MinioEndpoint = v }},
		{"MINIO_ACCESS_KEY", func(v string) { Options.MinioAccessKey = v }},
		{"MINIO_SECRET_KEY", func(v string) { Options.MinioSecretKey = v }},
		{"MINIO_BUCKET", func(v string) { Options.MinioBucket = v }},
	}
	for _, o := range overrides {
		if v, ok := lookup(o.env); ok {
			o.apply(v)
		}
	}
}
