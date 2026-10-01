# Build context: the repository root (docker build -f deployments/Dockerfile .).

# 1. Frontend: web/dist/app, which the API binary embeds.
FROM node:24.21.0-alpine AS web
RUN npm install --global pnpm@12.8.1
WORKDIR /src/web
COPY web/package.json web/pnpm-lock.yaml web/pnpm-workspace.yaml ./
RUN pnpm install --frozen-lockfile
COPY web/ ./
RUN pnpm build

# 2. Go binaries: static, the frontend embedded into api.
FROM golang:1.27.1-alpine AS go
ARG VERSION=dev
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /src/web/dist/app/ web/dist/app/
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/ ./cmd/api ./cmd/cli

# 3. Runtime: both binaries, no shell.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=go /out/api /out/cli /
EXPOSE 8080
ENTRYPOINT ["/api"]
