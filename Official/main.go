package main

import (
	"fmt"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/silenceper/wechat/v2"
	"github.com/silenceper/wechat/v2/cache"
	"github.com/silenceper/wechat/v2/officialaccount"
	"github.com/silenceper/wechat/v2/officialaccount/basic" // 【新增】引入 basic 包
	offConfig "github.com/silenceper/wechat/v2/officialaccount/config"
	"github.com/silenceper/wechat/v2/officialaccount/message"
	"net/http"
	"strings"
	"sync"
)

// 模拟数据库/Redis，用于存储 "场景值(SceneStr) -> 用户OpenID" 的映射
var loginCache sync.Map

// 全局微信实例
var oa *officialaccount.OfficialAccount

func initWechat() {
	wc := wechat.NewWechat()
	// 使用内存缓存，生产环境请用 Redis
	memory := cache.NewMemory()
	cfg := &offConfig.Config{
		AppID:          "你的AppID",
		AppSecret:      "你的AppSecret",
		Token:          "mytoken123",
		EncodingAESKey: "你的EncodingAESKey",
		Cache:          memory,
	}
	oa = wc.GetOfficialAccount(cfg)
}

func main() {
	initWechat()
	r := gin.Default()

	// 1. 获取登录二维码 【已修正】
	r.GET("/login/qrcode", func(c *gin.Context) {
		sceneStr := uuid.New().String()

		// 构造生成临时二维码的请求
		// 临时二维码 ActionName 为 QR_STR_SCENE (字符串参数)
		// 有效期 ExpireSeconds 设置为 600 秒
		req := &basic.Request{
			ExpireSeconds: 600,
			ActionName:    "QR_STR_SCENE",
			ActionInfo: struct {
				Scene struct {
					SceneStr string `json:"scene_str,omitempty"`
					SceneID  int    `json:"scene_id,omitempty"`
				} `json:"scene"`
			}{
				Scene: struct {
					SceneStr string `json:"scene_str,omitempty"`
					SceneID  int    `json:"scene_id,omitempty"`
				}{
					SceneStr: sceneStr,
				},
			},
		}

		// 调用 GetQRTicket
		qrRes, err := oa.GetBasic().GetQRTicket(req)
		if err != nil {
			fmt.Println("生成二维码失败:", err)
			c.JSON(500, gin.H{"error": err.Error()})
			return
		}

		// 返回给前端
		c.JSON(200, gin.H{
			"url":       qrRes.URL,    // 解析后的图片地址
			"ticket":    qrRes.Ticket, // 换取二维码图片的凭证
			"scene_str": sceneStr,     // 前端轮询的凭证
		})
	})

	// 2. 核心：微信服务器回调接口
	r.Any("/wx/callback", func(c *gin.Context) {
		server := oa.GetServer(c.Request, c.Writer)

		// 设置消息处理逻辑
		server.SetMessageHandler(func(msg *message.MixMessage) *message.Reply {
			// 只处理事件类型
			if msg.MsgType == message.MsgTypeEvent {
				var targetSceneStr string

				// 场景 A: 用户未关注，扫码 -> 触发 Subscribe 事件
				if msg.Event == message.EventSubscribe {
					// 微信推送的 EventKey 格式为 "qrscene_场景值"
					targetSceneStr = strings.Replace(msg.EventKey, "qrscene_", "", 1)
				}

				// 场景 B: 用户已关注，扫码 -> 触发 Scan 事件
				if msg.Event == message.EventScan {
					// 微信推送的 EventKey 直接就是 "场景值"
					targetSceneStr = msg.EventKey
				}

				// 如果拿到了场景值，说明扫码成功
				if targetSceneStr != "" {
					fmt.Printf("用户扫码成功！OpenID: %s, Scene: %s\n", msg.FromUserName, targetSceneStr)

					// 存入缓存
					loginCache.Store(targetSceneStr, string(msg.FromUserName))

					// 回复消息
					return &message.Reply{
						MsgType: message.MsgTypeText,
						MsgData: message.NewText("登录成功，欢迎回来！"),
					}
				}
			}
			return nil
		})

		if err := server.Serve(); err != nil {
			fmt.Println("处理微信回调失败:", err)
			return
		}
	})

	// 3. 前端轮询接口
	r.GET("/login/check", func(c *gin.Context) {
		sceneStr := c.Query("scene_str")

		if openID, ok := loginCache.Load(sceneStr); ok {
			// 登录成功，清除缓存
			loginCache.Delete(sceneStr)
			c.JSON(200, gin.H{
				"status": "ok",
				"openid": openID,
				"token":  "mock-jwt-token-" + sceneStr,
			})
		} else {
			c.JSON(200, gin.H{"status": "waiting"})
		}
	})

	// 启动前端页面
	r.LoadHTMLGlob("templates/*")
	r.GET("/", func(c *gin.Context) {
		c.HTML(http.StatusOK, "index.html", nil)
	})

	fmt.Println("服务启动在 :8080")
	r.Run(":8080")
}
