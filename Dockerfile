# ---- 构建 ----
FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /out/nxmem ./cmd/nxmem

# ---- 运行 ----
FROM alpine:3.20
RUN adduser -D -u 10001 nx
WORKDIR /app
COPY --from=build /out/nxmem /app/nxmem
COPY static /app/static
RUN mkdir -p /data && chown -R nx:nx /data /app
USER nx
ENV NXMEM_HOME=/data
EXPOSE 8861
ENTRYPOINT ["/app/nxmem"]
CMD ["serve", "--addr", "0.0.0.0:8861", "--static", "/app/static"]
