# syntax=docker/dockerfile:1

FROM --platform=$BUILDPLATFORM golang:1.22-alpine AS builder

WORKDIR /src

RUN apk add --no-cache ca-certificates

COPY go.mod ./
RUN go mod download

COPY . .

ARG TARGETOS
ARG TARGETARCH

ENV CGO_ENABLED=0

RUN GOOS="${TARGETOS}" GOARCH="${TARGETARCH}" \
    go build -trimpath -ldflags="-s -w" -o /out/costrict-router ./cmd/costrict-router

FROM alpine:3.22

RUN apk add --no-cache ca-certificates tzdata wget \
    && addgroup -S costrict-router \
    && adduser -S -G costrict-router -h /home/costrict-router costrict-router \
    && mkdir -p /data \
    && chown -R costrict-router:costrict-router /data /home/costrict-router

COPY --from=builder /out/costrict-router /usr/local/bin/costrict-router

USER costrict-router

ENV COSTRICT_ROUTER_CONFIG=/data/config.json

VOLUME ["/data"]
EXPOSE 14567

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD wget -qO- http://127.0.0.1:14567/healthz >/dev/null || exit 1

ENTRYPOINT ["costrict-router"]
CMD ["serve", "--addr", "0.0.0.0:14567"]
