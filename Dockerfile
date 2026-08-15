# --- build stage ---
FROM golang:1.25-alpine AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/api ./cmd/api

# --- run stage ---
FROM alpine:3.20

RUN apk add --no-cache ca-certificates
WORKDIR /app

COPY --from=build /out/api ./api

# Railway sets $PORT at runtime; the app already reads it via config.Load().
EXPOSE 8080
CMD ["./api"]
