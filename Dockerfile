FROM node:22-alpine AS frontend
WORKDIR /app/web
COPY web/ .
RUN npm ci && npm run build              # adapter-static output → build/

FROM golang:1.25-alpine AS backend
ARG GIT_SHA=
WORKDIR /app
COPY . .
COPY --from=frontend /app/web/build ./web/build
# web/build is go:embed'd in (web/embed.go); GIT_SHA is stamped in for the footer's version label
RUN go build -ldflags "-X hoardqr/internal/version.Commit=${GIT_SHA}" -o hoardqr ./cmd/hoardqr

FROM alpine:3.20
COPY --from=backend /app/hoardqr /app/hoardqr
ENTRYPOINT ["/app/hoardqr"]
