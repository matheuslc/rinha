FROM golang:1.20 as builder

LABEL maintainer = "Matheus Carmo (a.k.a Carmel) <mematheuslc@gmail.com>"

WORKDIR /app

COPY go.mod ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o /app/api main.go
EXPOSE 80

CMD /app/api

# Final image
FROM alpine:latest as runner
RUN apk --no-cache add ca-certificates

WORKDIR /app
COPY --from=builder /app/api .
EXPOSE 80

CMD /app/api