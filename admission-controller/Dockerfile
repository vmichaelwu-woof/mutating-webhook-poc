FROM golang:1.25 AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -a -installsuffix cgo -o mutating-webhook .

FROM scratch
COPY --from=builder /app/mutating-webhook /mutating-webhook

ENTRYPOINT ["/mutating-webhook"]