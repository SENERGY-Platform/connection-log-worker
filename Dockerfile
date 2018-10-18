FROM golang:1.11


COPY . /go/src/connection-log-worker
WORKDIR /go/src/connection-log-worker

ENV GO111MODULE=on

RUN go build

EXPOSE 8080

CMD ./connection-log-worker