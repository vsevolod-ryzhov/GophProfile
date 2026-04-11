## Useful commands
- docker-compose up -d
- docker-compose down
- go run cmd/server/main.go -d="host=localhost user=postgres_user password=postgres_password dbname=postgres_db sslmode=disable" -c="crt/server.crt" -k="crt/server.key"