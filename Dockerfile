# syntax=docker/dockerfile:1
FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/bfeed ./cmd/bfeed

FROM gcr.io/distroless/static:nonroot
COPY --from=build /out/bfeed /bfeed
ENV BFEED_DATABASE_PATH=/data/bfeed.db
VOLUME ["/data"]
EXPOSE 8080
USER nonroot:nonroot
HEALTHCHECK --interval=30s --timeout=3s CMD ["/bfeed", "healthcheck"]
ENTRYPOINT ["/bfeed"]
CMD ["serve"]
