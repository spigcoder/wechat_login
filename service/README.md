# 微信服务号扫码关注登录

该示例演示用户扫描服务号二维码，完成 OAuth2 网页授权并确认已关注后才允许登录。

## 环境准备

1. 使用已认证的微信服务号，记录 `AppID` 与 `AppSecret`，并在公众平台配置网页授权回调域名。
2. 将服务域名解析到可公网访问的地址，保证 `https://example.com/wechat/callback` 等回调链接可被访问。
3. 导出以下环境变量：

```bash
export WECHAT_SERVICE_APP_ID=你的服务号AppID
export WECHAT_SERVICE_APP_SECRET=你的服务号AppSecret
export WECHAT_SERVICE_CALLBACK_URL=https://example.com/wechat/callback
export PORT=8080 # 可选
```

## 运行方式

```bash
GOCACHE=$(pwd)/.cache go run ./service
```

根路由 `/` 会生成 `state` 并跳转至 `https://open.weixin.qq.com/connect/oauth2/authorize` 的网页授权页面。用户扫码授权后，`/wechat/callback` 会先换取 OAuth `access_token` 与基础用户信息，再调用 `cgi-bin/user/info` 校验 `subscribe==1`，只有关注了服务号才返回欢迎信息。

## 细节说明

- 全局 `access_token` 采用内存缓存，距离过期 60 秒内会自动刷新。
- HTTP 请求均设置 10 秒超时，并显式检查 `errcode/errmsg` 方面的错误。
- 若要将登录态写入自身系统，可在 `handleCallback` 中获取到 `user` 以及关注状态后执行后续逻辑。

参考文档：
- [网页授权流程](https://developers.weixin.qq.com/doc/offiaccount/OA_Web_Apps/Wechat_webpage_authorization.html)
- [获取用户基本信息](https://developers.weixin.qq.com/doc/offiaccount/User_Management/Get_users_basic_information_UnionID.html)
