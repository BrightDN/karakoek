FROM golang:1.22-alpine AS build
WORKDIR /src
COPY go.mod go.sum* ./
COPY . .
RUN go mod tidy && go mod download
RUN CGO_ENABLED=0 go build -o /out/karakoek ./cmd/server

FROM gcr.io/distroless/static-debian12
WORKDIR /app
COPY --from=build /out/karakoek /app/karakoek
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/app/karakoek"]
