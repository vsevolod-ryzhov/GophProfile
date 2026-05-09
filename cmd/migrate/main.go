// Command migrate applies database migrations and exits.
// Used as the Helm pre-install / pre-upgrade hook for the gophprofile chart.
// The DSN is read from the DATABASE_DSN environment variable.
package main

import (
	"log"
	"os"

	"GophProfile/internal/storage"
)

func main() {
	dsn := os.Getenv("DATABASE_DSN")
	if dsn == "" {
		log.Fatal("DATABASE_DSN is required")
	}
	if err := storage.ApplyMigrationsDSN(dsn); err != nil {
		log.Fatalf("apply migrations: %v", err)
	}
	log.Println("migrations applied")
}
