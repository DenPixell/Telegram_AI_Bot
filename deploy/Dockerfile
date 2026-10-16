FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server

# distroless: без shell, запуск не от root. tzdata вшита в бинарник (time/tzdata).
FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=build /out/server /app/server
COPY config /app/config
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/app/server"]
