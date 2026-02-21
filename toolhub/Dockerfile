FROM golang:1.25-alpine AS build
ARG HTTPS_PROXY
ARG HTTP_PROXY
ARG NO_PROXY
ARG https_proxy
ARG http_proxy
ARG no_proxy
ENV HTTPS_PROXY=${HTTPS_PROXY} \
    HTTP_PROXY=${HTTP_PROXY} \
    NO_PROXY=${NO_PROXY} \
    https_proxy=${https_proxy} \
    http_proxy=${http_proxy} \
    no_proxy=${no_proxy}
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/toolhub ./cmd/toolhub

FROM alpine:3.20
RUN adduser -D -u 10001 toolhub
USER toolhub
WORKDIR /home/toolhub
COPY --from=build /out/toolhub /usr/local/bin/toolhub
EXPOSE 8080 8090
ENTRYPOINT ["toolhub"]
