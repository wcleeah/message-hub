set dotenv-load

dev:
    go run cmd/server/main.go

test-all:
    go test ./... -v

fb:
	go run cmd/testutil/framebinary/main.go

btxb bin:
	printf '%02X\n' "$((2#{{bin}}))"
