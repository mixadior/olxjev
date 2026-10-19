# Build stage: a static olxjev binary (templates, assets and OLX form data
# are embedded, so the binary is all the runtime image needs).
FROM golang:1.27-alpine AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/olxjev ./cmd/olxjev

# Runtime stage: distroless static ships CA certificates (the decision APIs
# and OLX are HTTPS) and runs as an unprivileged user.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/olxjev /usr/local/bin/olxjev
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/olxjev"]
# The web UI by default; pass CLI flags instead to resolve one query, e.g. -q "...".
CMD ["-serve", "0.0.0.0:8080"]
