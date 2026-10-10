# Rivus Agent 后端镜像（CGO-free，alpine 运行时）
# 构建：docker build -t rivus-agent:latest ./backend
# 运行：docker run -d -p 8080:8080 \
#         -e MODEL_BASE_URL=... -e MODEL_API_KEY=... -e MODEL_NAME=... \
#         -v rivus-data:/data rivus-agent:latest
# 可通过 DOCKER_REGISTRY 指定镜像源（如 registry.1ms.run/）加速国内拉取
ARG REGISTRY=
ARG GO_VERSION=1.27

FROM ${REGISTRY}golang:${GO_VERSION}-bookworm AS builder
WORKDIR /src
ENV CGO_ENABLED=0 GOFLAGS=-mod=mod GOPROXY=https://goproxy.cn,direct
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -trimpath -ldflags="-s -w" -o /out/agentd ./cmd/agentd

FROM ${REGISTRY}alpine:3.21
RUN apk add --no-cache ca-certificates tzdata wget \
  && adduser -D -H -u 10001 appuser \
  && mkdir -p /data /app/skills /app/configs && chown -R appuser /data /app
COPY --from=builder /out/agentd /app/agentd
COPY --chown=appuser configs/config.example.yaml /app/configs/config.example.yaml
COPY --chown=appuser skills/ /app/skills/
USER appuser
WORKDIR /app
ENV AGENT_ADDR=:8080 \
  AGENT_DB_PATH=/data/agent.db \
  AGENT_SKILLS_DIR=/app/skills \
  WORKER_CONCURRENCY=1 \
  LOG_LEVEL=info
VOLUME /data
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD wget -qO- http://127.0.0.1:8080/healthz | grep -q '"status":"ok"'
ENTRYPOINT ["/app/agentd", "-config", "/app/configs/config.example.yaml"]
