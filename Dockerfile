FROM golang:1.25 AS build-stage

WORKDIR /app

COPY go.mod go.sum ./

# RUN go mod download

# Copy the source code. Note the slash at the end, as explained in
# https://docs.docker.com/reference/dockerfile/#copy
COPY *.go ./

# Build
RUN CGO_ENABLED=0 GOOS=linux go build -o /test/webhook

COPY certs /test/

FROM gcr.io/distroless/base-debian11 AS build-release-stage

WORKDIR /

COPY --from=build-stage /test/webhook /webhook
COPY --from=build-stage /test/webhook.crt /certs/webhook.crt
COPY --from=build-stage /test/webhook.key /certs/webhook.key

# Optional:
# To bind to a TCP port, runtime parameters must be supplied to the docker command.
# But we can document in the Dockerfile what ports
# the application is going to listen on by default.
# https://docs.docker.com/reference/dockerfile/#expose
EXPOSE 8443

# Run
CMD ["/webhook", "--tls-cert", "/certs/webhook.crt", "--tls-key", "/certs/webhook.key"]
