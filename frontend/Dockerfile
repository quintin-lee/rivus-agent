# Rivus Agent 前端镜像（多阶段构建：pnpm 构建 + nginx 托管）
# 构建：docker build -t rivus-agent-frontend:latest ./frontend
# 运行：docker run -d -p 8081:8080 rivus-agent-frontend:latest
#
# 生产部署时，/api 反向代理到后端镜像：
#   docker run -d -p 8081:8080 \
#     -e RIVUS_BACKEND=http://host.docker.internal:8080 \
#     rivus-agent-frontend:latest

ARG NODE_VERSION=22

# ---- 阶段 1：构建静态资源 ----
FROM node:${NODE_VERSION}-alpine AS builder
WORKDIR /src
ENV PNPMDIR=/root/.local/share/pnpm
RUN corepack enable pnpm
COPY package.json pnpm-lock.yaml ./
RUN pnpm install
COPY . .
RUN pnpm build

# ---- 阶段 2：nginx 托管 + API 反向代理 ----
FROM nginx:1.27-alpine
RUN apk add --no-cache gettext
COPY nginx.conf.template /etc/nginx/conf.d/default.conf.template
COPY start.sh /usr/local/bin/start.sh
RUN chmod +x /usr/local/bin/start.sh
COPY --from=builder /src/dist /usr/share/nginx/html
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/start.sh"]
