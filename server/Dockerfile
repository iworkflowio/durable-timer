FROM golang:1.19 AS build
WORKDIR /go/src
COPY genapi/go ./go
COPY main.go .
COPY go.sum .
COPY go.mod .

ENV CGO_ENABLED=0

RUN go build -o genapi .

FROM scratch AS runtime
ENV GIN_MODE=release
COPY --from=build /go/src/genapi ./
EXPOSE 8080/tcp
ENTRYPOINT ["./genapi"]
