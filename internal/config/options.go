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

	if envRunAddr, exists := os.LookupEnv("SERVER_ADDRESS"); exists {
		Options.AppPort = envRunAddr
	}

	if envDatabaseDSN, exists := os.LookupEnv("DATABASE_DSN"); exists {
		Options.DatabaseDSN = envDatabaseDSN
	}

	if certFile, exists := os.LookupEnv("SERVER_CERT"); exists {
		Options.CertFile = certFile
	}

	if keyFile, exists := os.LookupEnv("SERVER_KEY"); exists {
		Options.KeyFile = keyFile
	}

	if v, exists := os.LookupEnv("MINIO_ENDPOINT"); exists {
		Options.MinioEndpoint = v
	}

	if v, exists := os.LookupEnv("MINIO_ACCESS_KEY"); exists {
		Options.MinioAccessKey = v
	}

	if v, exists := os.LookupEnv("MINIO_SECRET_KEY"); exists {
		Options.MinioSecretKey = v
	}

	if v, exists := os.LookupEnv("MINIO_BUCKET"); exists {
		Options.MinioBucket = v
	}
}
