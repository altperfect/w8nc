FROM node:24-alpine AS web
WORKDIR /src/web
COPY web/package*.json ./
RUN npm install
COPY web/ ./
RUN npm run build

FROM golang:1.25-alpine AS build
ARG NOTIFY_VERSION=v1.0.7
RUN apk add --no-cache ca-certificates git
RUN go install github.com/projectdiscovery/notify/cmd/notify@${NOTIFY_VERSION}
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN rm -rf internal/static/dist
COPY --from=web /src/web/dist ./internal/static/dist
RUN go build -o /out/endpoint-pinger ./cmd/server

FROM alpine:3.22
RUN apk add --no-cache ca-certificates tzdata
WORKDIR /app
COPY --from=build /out/endpoint-pinger /app/endpoint-pinger
COPY --from=build /go/bin/notify /usr/local/bin/notify
COPY migrations /app/migrations
RUN mkdir -p /app/data
EXPOSE 8080
CMD ["/app/endpoint-pinger"]
