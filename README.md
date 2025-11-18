# 微信扫码登录合集

该仓库包含两套最小可运行的 Go 示例，分别覆盖：

- `web/`：面向网站应用的微信扫码登录（开放平台二维码 `qrconnect`）。
- `service/`：微信服务号扫码关注登录，校验用户已关注后才允许登录。

## 运行方式

两套示例共享根目录下的 `go.mod`，可通过 `go run` + 子目录直接运行：

```bash
GOCACHE=$(pwd)/.cache go run ./web      # 网站扫码登录
GOCACHE=$(pwd)/.cache go run ./service  # 服务号关注登录
```

具体的环境变量、流程说明以及调试要点，请分别查阅 `web/README.md` 与 `service/README.md`。
