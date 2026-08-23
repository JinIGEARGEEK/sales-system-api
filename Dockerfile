# --- build stage ---
FROM golang:1.25-alpine AS build

WORKDIR /src

# gcc/musl-dev: needed to cgo-link github.com/gen2brain/go-fitz (MuPDF, used
# for FlowAccount PDF quote extraction) — it ships its own static MuPDF
# libs per platform, so no separate MuPDF package/runtime dependency is
# needed, just a C toolchain at build time.
RUN apk add --no-cache gcc musl-dev

COPY go.mod go.sum ./
RUN go mod download

COPY . .
# -tags musl selects go-fitz's musl-linked static MuPDF lib, matching this
# image's libc (alpine). CGO_ENABLED=1 is required for it; the resulting
# binary is still self-contained (MuPDF is statically linked in).
RUN CGO_ENABLED=1 GOOS=linux go build -tags musl -o /out/api ./cmd/api

# --- run stage ---
FROM alpine:3.20

RUN apk add --no-cache ca-certificates \
    && addgroup -S app && adduser -S -G app app
WORKDIR /app

COPY --from=build /out/api ./api
RUN chown app:app ./api
USER app

# Railway sets $PORT at runtime; the app already reads it via config.Load().
EXPOSE 8080
CMD ["./api"]
