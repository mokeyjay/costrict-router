# 部署与构建

## 自行构建

```bash
git clone https://github.com/mokeyjay/costrict-router.git
cd costrict-router
go build -o costrict-router ./cmd/costrict-router
```

## Docker 镜像

容器默认使用 `/data/config.json` 保存登录态和本地 API Key。可以先创建一个 volume：

```bash
docker volume create costrict-router-data
```

### 1. 首次登录 CoStrict

```bash
docker run --rm -it \
  -v costrict-router-data:/data \
  ghcr.io/mokeyjay/costrict-router:latest \
  login --base-url https://www.abc.com
```

### 2. 生成本地 API Key：

```bash
docker run --rm -it \
  -v costrict-router-data:/data \
  ghcr.io/mokeyjay/costrict-router:latest \
  key reset
```

`key reset` 输出的 `sk-costrict-...` 只会显示一次，请立即保存。

### 3. 启动代理服务：

```bash
docker run -d --name costrict-router \
  -p 14567:14567 \
  -v costrict-router-data:/data \
  ghcr.io/mokeyjay/costrict-router:latest
```

之后就能愉快使用 `http://[宿主机 ip]:14567/v1` 啦～
