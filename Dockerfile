
FROM golang:1.16-alpine
RUN apk update && apk add --no-cache ca-certificates git

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download 
COPY . .
RUN go build -o main .
EXPOSE 8080

CMD ["./main"]
