# syntax=docker/dockerfile:1
FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/shinsou-server ./cmd/shinsou-server

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/shinsou-server /usr/local/bin/shinsou-server
ENV SHINSOU_LISTEN=0.0.0.0:8080
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/shinsou-server"]
CMD ["serve"]
