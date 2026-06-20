FROM node:20-alpine AS web-assets
WORKDIR /web
COPY clients/web/package*.json ./
RUN npm ci
COPY clients/web/ .
RUN npm run build

FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build
ARG TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN GOARCH=$TARGETARCH CGO_ENABLED=0 go build -o /out/ ./cmd/...

FROM build AS dashboard-build
ARG TARGETARCH
COPY --from=web-assets /web/dist internal/dashboard/dist
RUN GOARCH=$TARGETARCH CGO_ENABLED=0 go build -o /out/hookrail-dashboard ./cmd/hookrail-dashboard

FROM alpine:3.20 AS dashboard
RUN apk add --no-cache ca-certificates
COPY --from=dashboard-build /out/hookrail-dashboard /usr/local/bin/hookrail-dashboard
USER 65532:65532

FROM alpine:3.20
RUN apk add --no-cache ca-certificates
COPY --from=build /out/ /usr/local/bin/
USER 65532:65532
# entrypoint chosen per service in compose
# NOTE: dashboard target is above (multi-stage with web-assets)
