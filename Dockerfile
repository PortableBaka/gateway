# ---- build stage ----
FROM golang:1.26 AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

# CGO_ENABLED=0: a statically linked binary with no libc dependency. This is
# what makes the distroless base image below possible — it has no C library
# at all, so a dynamically linked binary simply wouldn't run in it.
RUN CGO_ENABLED=0 GOOS=linux go build -o /gateway ./cmd/gateway

# ---- final stage ----
FROM gcr.io/distroless/static-debian12:nonroot

WORKDIR /app

COPY --from=build /gateway /app/gateway
COPY config.yaml /app/config.yaml

EXPOSE 8081

ENTRYPOINT ["/app/gateway"]
