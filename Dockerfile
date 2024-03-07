FROM golang:1.21-bullseye

WORKDIR /app

COPY go.mod ./
COPY go.sum ./
RUN go mod download

COPY *.go ./

RUN go build -o /someguy

CMD [ "/someguy", "start" ]
