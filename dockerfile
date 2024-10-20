# 使用官方 Golang 镜像作为基础镜像
FROM golang:latest AS builder

# 设置工作目录
WORKDIR /app

# 将 go.mod 和 go.sum 复制到工作目录中
COPY go.mod go.sum ./

# 下载并缓存依赖项
RUN go mod download

# 将其余的项目代码复制到容器中
COPY . .

# 构建二进制文件
RUN go build -o server .

# 使用轻量级的基础镜像
FROM alpine:latest

# 设置工作目录
WORKDIR /root/

# 从构建阶段复制编译后的二进制文件
COPY --from=builder /app/server .

# 暴露 gRPC 服务的端口
EXPOSE 50051

# 启动应用程序
CMD ["./server"]
