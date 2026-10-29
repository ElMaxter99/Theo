FROM golang:1.24-alpine AS build
WORKDIR /src
COPY go.mod main.go ./
ARG BRANCH=unknown
RUN CGO_ENABLED=0 go build -ldflags "-X main.branch=${BRANCH}" -o /demo .

FROM alpine:3.20
COPY --from=build /demo /usr/local/bin/demo
RUN mkdir /data
ENTRYPOINT ["demo"]
