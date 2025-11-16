FROM golang:1.24

WORKDIR /app

COPY go.mod go.sum ./

# RUN go mod download

# Copy the source code. Note the slash at the end, as explained in
# https://docs.docker.com/reference/dockerfile/#copy
COPY *.go ./

# Build
RUN CGO_ENABLED=0 GOOS=linux go build -o /test/webhook

COPY certs /test/

# Optional:
# To bind to a TCP port, runtime parameters must be supplied to the docker command.
# But we can document in the Dockerfile what ports
# the application is going to listen on by default.
# https://docs.docker.com/reference/dockerfile/#expose
EXPOSE 8443

# Run
CMD ["/test/webhook --tls-cert /test/tls.crt --tls-key /test/tls.key"]
