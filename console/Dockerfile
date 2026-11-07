FROM node:26-bookworm-slim@sha256:662933cf47f013bc8e4beb31a6116448427a82057ba7c42c97e4c5ba766504c2 AS web
WORKDIR /web
COPY console/web/package.json console/web/package-lock.json ./
RUN npm ci
COPY console/web ./
COPY console/scripts/sbom-web.mjs /scripts/sbom-web.mjs
RUN npm run build
FROM golang:1.27.3-alpine@sha256:d200d2dd76b03a99b52651fd1325aff7dcd35422c35c01bac56571b0507f3deb AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY console ./console
COPY internal ./internal
COPY api ./api
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/authova-console ./console/cmd/authova-console
FROM alpine:3.22@sha256:5291449c3df73caf6ed85e649dec1b9e818b39a5d8c871e97afc13e9cd5e8fa8
RUN apk add --no-cache ca-certificates && addgroup -g 10001 authova && adduser -D -u 10001 -G authova authova
COPY --from=build /out/authova-console /usr/local/bin/authova-console
COPY --from=web /web/dist /opt/authova-console/web
ENV AUTHOVA_CONSOLE_ASSETS=/opt/authova-console/web
USER 10001:10001
EXPOSE 8090
ENTRYPOINT ["authova-console"]
