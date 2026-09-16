FROM node:22-alpine AS frontend
WORKDIR /app/web
COPY web/ .
RUN npm ci && npm run build              # adapter-static output → build/

FROM golang:1.23-alpine AS backend
WORKDIR /app
COPY --from=frontend /app/web/build ./web/build
COPY . .
RUN go build -o hoardqr ./cmd/hoardqr    # web/build is go:embed'd in, starting Phase 2

FROM alpine:3.20
COPY --from=backend /app/hoardqr /app/hoardqr
ENTRYPOINT ["/app/hoardqr"]
