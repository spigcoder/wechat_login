# 微信服务号扫码关注登录（网页二维码）

该示例演示网页端自行生成服务号二维码，用户用微信客户端扫描后会跳转到服务号主页，关注成功后再允许网页端登录。

## 流程概览

1. 网页调用 `POST /session/new` 获取一次性登录会话，响应中包含 `scene` 与 `qrcode_url`。
2. 前端把 `qrcode_url` 渲染成二维码图片。用户用手机微信扫描后会进入服务号。
3. 若用户未关注，会在服务号主页完成关注；关注或扫码事件会通过 `/wechat/message` 回调到本服务。
4. 服务器收到事件后，调用 `cgi-bin/user/info` 校验 `subscribe==1`，并把登录会话状态更新为 `subscribed`。
5. 前端轮询 `GET /session/{scene}`，一旦状态变为 `subscribed` 即可创建业务登录态。

## 环境准备

1. 使用已认证的微信服务号，记录 `AppID`、`AppSecret`，并在公众平台**开发 > 基本配置**里设置服务器地址（指向 `/wechat/message`）和自定义 `Token`。
2. 导出以下环境变量并启动服务：

```bash
export WECHAT_SERVICE_APP_ID=服务号AppID
export WECHAT_SERVICE_APP_SECRET=服务号AppSecret
export WECHAT_SERVICE_TOKEN=公众平台配置的 Token
export PORT=8080 # 可选

GOCACHE=$(pwd)/.cache go run ./service
```

## 可用接口

| 方法 | 路径 | 说明 |
| ---- | ---- | ---- |
| `POST /session/new` | 创建一次登录会话并返回二维码地址 |
| `GET /session/{scene}` | 查询指定 `scene` 的登录状态（`pending` / `subscribed`） |
| `GET/POST /wechat/message` | 供微信服务器验证及推送订阅事件，需在公众号后台配置 |

## 细节

- 二维码通过 `cgi-bin/qrcode/create` 创建临时 `QR_STR_SCENE`，默认 300 秒过期。
- 服务端内存缓存 `access_token` 并自动刷新，同时会定期清理过期会话。
- `wechat/message` 仅处理 `subscribe` / `SCAN` 事件，其它事件会被忽略，回调响应固定 `success`。
- 登录成功后 `GET /session/{scene}` 会返回 `openid`、`nickname`，可据此建立站点自身会话。

参考文档：
- [生成带参数二维码](https://developers.weixin.qq.com/doc/offiaccount/Account_Management/Generating_a_Parametric_QR_Code.html)
- [接收事件推送](https://developers.weixin.qq.com/doc/offiaccount/Message_Management/Receiving_event_pushes.html)
