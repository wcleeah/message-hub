set dotenv-load

dev:
    go run cmd/server/main.go

test-all:
    go test ./... -v

fb:
	go run cmd/util/framebinary.go

btxb bin:
	printf '%02X\n' "$((2#{{bin}}))"
