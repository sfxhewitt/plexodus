.PHONY: build web server test dev clean

build: web server

web:
	cd web && npm ci && npm run build

server:
	CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o plexodus ./cmd/plexodus

test:
	go vet ./... && go test ./...
	cd web && npm run typecheck

# Backend on :8484 proxying the UI from Vite's dev server on :5173.
dev:
	go run ./cmd/plexodus -dev-proxy http://localhost:5173 & cd web && npm run dev

clean:
	rm -f plexodus && find web/dist -mindepth 1 ! -name .gitkeep -delete
