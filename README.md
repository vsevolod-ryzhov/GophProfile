## Useful commands
- docker-compose up -d
- docker-compose down
- go run cmd/server/main.go -d="host=localhost user=postgres_user password=postgres_password dbname=postgres_db sslmode=disable" -c="crt/server.crt" -k="crt/server.key"
- go test $(go list ./...) -coverprofile=coverage.out && go tool cover -func=coverage.out | grep total


## OK
```shell
curl -v --cacert crt/ca.crt -H "X-User-ID: test-user-1" -F "image=@test-data/example1.jpg" https://localhost:8080/api/v1/avatars
```

## Missing header
```shell
curl -v --cacert crt/ca.crt -F "image=@test-data/example1.jpg" https://localhost:8080/api/v1/avatars
```

## Wrong field name (expect 400 MissingFileField):
```shell
curl -v --cacert crt/ca.crt -H "X-User-ID: test-user-1" -F "file=@test-data/example1.jpg" https://localhost:8080/api/v1/avatars
```

## Fake image (text file with image extension — expect 400 UnsupportedMediaType, this is the magic-byte check earning its keep):
```shell
echo "not an image" > /tmp/fake.png && curl -v --cacert crt/ca.crt -H "X-User-ID: test-user-1" -F "image=@/tmp/fake.png" https://localhost:8080/api/v1/avatars
```

## Oversized file (expect 413 FileTooLarge):
```shell
dd if=/dev/urandom of=/tmp/big.bin bs=1m count=15 && curl -v --cacert crt/ca.crt -H "X-User-ID: test-user-1" -F "image=@/tmp/big.bin" https://localhost:8080/api/v1/avatars
```

## Not multipart at all (expect 400 ExpectedMultipartFormData):
```shell
curl -v --cacert crt/ca.crt -H "X-User-ID: test-user-1" -H "Content-Type: application/json" -d '{"foo":"bar"}' https://localhost:8080/api/v1/avatars
```           