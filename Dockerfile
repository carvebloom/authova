FROM golang:1.27.4-alpine@sha256:0a5f7fe796599ff487605987432e7619ae45beeb2fba4c2ce0b907879932aebf AS build
ARG VERSION=0.1.0-rc.1
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
COPY api ./api
RUN GOMAXPROCS=2 CGO_ENABLED=0 go build -p=2 -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/authova ./cmd/authova
FROM alpine:3.22@sha256:5291449c3df73caf6ed85e649dec1b9e818b39a5d8c871e97afc13e9cd5e8fa8
RUN apk add --no-cache ca-certificates && addgroup -g 10001 authova && adduser -D -u 10001 -G authova authova
COPY --from=build /out/authova /usr/local/bin/authova
USER 10001:10001
EXPOSE 8080
ENTRYPOINT ["authova"]
CMD ["serve"]
