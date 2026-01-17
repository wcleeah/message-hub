set dotenv-load

dev:
    go run cmd/server/main.go

test-all:
    go test ./... -v

btx bin:
	printf '%02X\n' "$((2#{{bin}}))"
