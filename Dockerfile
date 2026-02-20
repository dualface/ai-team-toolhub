FROM golang:1.23-alpine AS build
WORKDIR /src
COPY . .
RUN go build -o /out/toolhub ./cmd/toolhub

FROM alpine:3.20
RUN adduser -D -u 10001 toolhub
USER toolhub
WORKDIR /home/toolhub
COPY --from=build /out/toolhub /usr/local/bin/toolhub
EXPOSE 8080 8090
ENTRYPOINT ["toolhub"]
