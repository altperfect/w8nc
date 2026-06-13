FROM node:24-alpine AS web
WORKDIR /src/web
COPY web/package*.json ./
RUN npm install
COPY web/ ./
RUN npm run build

FROM golang:1.26-alpine AS build
ARG NOTIFY_VERSION=v1.0.7
RUN apk add --no-cache ca-certificates git
RUN go install github.com/projectdiscovery/notify/cmd/notify@${NOTIFY_VERSION}
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN rm -rf internal/static/dist
COPY --from=web /src/web/dist ./internal/static/dist
RUN go build -o /out/w8nc ./cmd/server

FROM alpine:3.22
RUN apk add --no-cache ca-certificates tzdata chromium font-noto
ENV HOME=/tmp
WORKDIR /app
COPY --from=build /out/w8nc /app/w8nc
COPY --from=build /go/bin/notify /usr/local/bin/notify
COPY migrations /app/migrations
RUN addgroup -S -g 10001 w8nc \
    && adduser -S -D -H -u 10001 -G w8nc w8nc \
    && mkdir -p /app/data \
    && chown -R w8nc:w8nc /app
USER w8nc:w8nc
EXPOSE 8080
CMD ["/app/w8nc"]
