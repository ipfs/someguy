FROM golang:1.18-bullseye

WORKDIR /app

COPY go.mod ./
COPY go.sum ./
RUN go mod download

COPY *.go ./

RUN go build -o /someguy

EXPOSE 8080

CMD [ "/someguy", "start" ]
