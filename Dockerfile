# Build stage: compile a static binary.
FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /bin/bank-api ./cmd/server

# Run stage: minimal image, no toolchain, non-root user.
FROM alpine:3.20
RUN adduser -D -u 10001 app
USER app
COPY --from=build /bin/bank-api /usr/local/bin/bank-api
EXPOSE 8000
ENTRYPOINT ["bank-api"]
