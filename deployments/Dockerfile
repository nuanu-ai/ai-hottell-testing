# Build from the public repository; client files are bundled for offline serving.
FROM alpine:3.22 AS client
RUN apk add --no-cache curl
ARG CLIENT_RELEASE_URL=https://github.com/nuanu-ai/ai-hottell-testing/releases/download/v0.3.6
WORKDIR /releases
RUN curl --retry 3 --connect-timeout 15 --max-time 180 -fsSL "$CLIENT_RELEASE_URL/hottell-darwin-arm64" -o hottell-darwin-arm64 \
 && curl --retry 3 --connect-timeout 15 --max-time 180 -fsSL "$CLIENT_RELEASE_URL/hottell-darwin-amd64" -o hottell-darwin-amd64 \
 && curl --retry 3 --connect-timeout 15 --max-time 180 -fsSL "$CLIENT_RELEASE_URL/SHA256SUMS" -o SHA256SUMS \
 && sha256sum -c SHA256SUMS
COPY deployments/hottell/install.sh ./install.sh

FROM node:24.21.0-alpine AS web
RUN npm install --global pnpm@12.8.1
WORKDIR /src/web
COPY web/package.json web/pnpm-lock.yaml web/pnpm-workspace.yaml ./
RUN pnpm install --frozen-lockfile
COPY web/ ./
RUN pnpm typecheck && pnpm lint && pnpm build

FROM golang:1.27.1-alpine AS go
ARG VERSION=0.3.6-selfhost
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ cmd/
COPY internal/ internal/
COPY pkg/ pkg/
COPY skills/ skills/
COPY database/ database/
COPY web/embed.go web/embed.go
COPY --from=web /src/web/dist/app/ web/dist/app/
RUN CGO_ENABLED=0 go build -buildvcs=false -trimpath -ldflags "-s -w -X main.version=$VERSION" -o /out/ ./cmd/api ./cmd/cli

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=go /out/api /out/cli /
COPY --from=client /releases/ /opt/hottell/releases/
EXPOSE 8080
ENTRYPOINT ["/api"]
