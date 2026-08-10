FROM golang:1.26 AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .

ARG VERSION=dev
ARG GIT_COMMIT=unknown
RUN CGO_ENABLED=0 go build -trimpath \
	-ldflags "-s -w -X main.version=${VERSION} -X main.gitCommit=${GIT_COMMIT}" \
	-o /out/distribution ./cmd/distribution

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/distribution /distribution
COPY config.example.toml /config.example.toml

USER nonroot:nonroot
EXPOSE 8080 9090
ENTRYPOINT ["/distribution"]
