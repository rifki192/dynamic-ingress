# build stage
FROM golang:alpine AS build-env
RUN apk --no-cache add build-base git bzr mercurial gcc
ADD . /src
RUN cd /src && go build -ldflags="-s -w" -o dynamic-ingress

# Get certs stage
FROM alpine as certs
RUN apk --update add ca-certificates

# final stage
FROM alpine
WORKDIR /app
COPY --from=build-env /src/dynamic-ingress /app/
COPY --from=certs /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
ENTRYPOINT ./dynamic-ingress