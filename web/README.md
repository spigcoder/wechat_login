# 网页版微信扫码登录示例

该示例提供一个最小可运行的 Go Web 服务，演示网页端拉起微信扫码登录、完成 OAuth2 回调后输出 `hello_world` 的流程。

## 环境准备

1. 在[微信开放平台](https://open.weixin.qq.com/)创建网站应用，记录 `AppID` 与 `AppSecret`。
2. 在开放平台配置授权回调域名（如 `example.com`），并确保 `https://example.com/wechat/callback` 可被公网访问。
3. 在本地或服务器导出以下环境变量：

```bash
export WECHAT_APP_ID=你的AppID
export WECHAT_APP_SECRET=你的AppSecret
export WECHAT_CALLBACK_URL=https://example.com/wechat/callback
export PORT=8080 # 可选，默认 8080
```

## 运行服务

```bash
GOCACHE=$(pwd)/.cache go run ./web
```

启动后访问 `http://localhost:8080/`，服务会自动重定向至微信扫码登录页，扫码授权成功后回调 `hello_world` 文案，并在结尾输出当前登录用户的 `openid` 与昵称。

## 关键点说明

- 根路由 `/`：生成随机 `state` 写入安全 Cookie，并跳转至 `https://open.weixin.qq.com/connect/qrconnect`。
- 回调 `/wechat/callback`：校验 `state`、使用临时 `code` 换取 `access_token`、再拉取用户信息。
- 所有与微信的 HTTP 请求都设置了 10 秒超时，并对 `errcode/errmsg` 做了显式校验，方便排错。

如需将登录态写入项目自身的会话系统，可在 `handleCallback` 中拿到用户信息后自行扩展逻辑。

参考文档：[微信登录](https://developers.weixin.qq.com/doc/oplatform/Website_App/WeChat_Login/Wechat_Login.html)
