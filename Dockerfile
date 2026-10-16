FROM golang:1.22-alpine AS build
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
ARG TARGET=bot
RUN CGO_ENABLED=0 go build -o /app ./cmd/${TARGET}

FROM alpine:3.20
RUN apk add --no-cache tzdata ca-certificates
COPY --from=build /app /app
COPY web /web
ENTRYPOINT ["/app"]
