FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/ ./cmd/...

FROM alpine:3.20
RUN apk add --no-cache ca-certificates
COPY --from=build /out/ /usr/local/bin/
USER 65532:65532
# entrypoint chosen per service in compose
