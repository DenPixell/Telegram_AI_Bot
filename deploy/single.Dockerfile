# Один контейнер: API + бот + панель. Для хостингов с одним сервисом (Render free и т.п.).
# В docker compose используются отдельные образы (deploy/Dockerfile и web/Dockerfile с nginx).

FROM node:22-alpine AS web
WORKDIR /web
COPY web/package.json web/package-lock.json ./
RUN npm ci --no-fund --no-audit
COPY web/ ./
RUN npm run build

FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=build /out/server /app/server
COPY config /app/config
COPY --from=web /web/dist /app/web
ENV WEB_DIR=/app/web
EXPOSE 10000
USER nonroot:nonroot
ENTRYPOINT ["/app/server"]
