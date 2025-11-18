package main

import (
	"bytes"
	"crypto/rand"
	"crypto/sha1"
	"embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	stateLength      = 24
	sessionStatusNew = "pending"
	sessionStatusOK  = "subscribed"
	qrExpireSeconds  = 300
)

var (
	appID         = os.Getenv("WECHAT_APP_ID")
	appSecret     = os.Getenv("WECHAT_APP_SECRET")
	token         = os.Getenv("WECHAT_TOKEN")
	listenAddr    = ":" + envOrDefault("PORT", "8080")
	httpClient    = &http.Client{Timeout: 10 * time.Second}
	errMissingEnv = errors.New("请设置 WECHAT_APP_ID、WECHAT_APP_SECRET 与 WECHAT_TOKEN 环境变量")

	globalTokenMu      sync.RWMutex
	globalToken        string
	globalTokenExpires time.Time

	loginSessions sync.Map // map[string]*loginSession

	//go:embed static
	staticFiles embed.FS
)

type loginSession struct {
	Scene     string    `json:"scene"`
	QRCodeURL string    `json:"qrcode_url"`
	Status    string    `json:"status"`
	OpenID    string    `json:"openid,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

func main() {
	if appID == "" || appSecret == "" || token == "" {
		log.Fatal(errMissingEnv)
	}

	http.Handle("/", http.FileServer(mustStatic()))
	http.HandleFunc("/session/new", handleCreateSession)
	http.HandleFunc("/session/", handleSessionStatus)
	http.HandleFunc("/wechat/message", handleMessage)

	go cleanupExpiredSessions()

	log.Printf("服务号扫码关注登录示例启动，监听 %s", listenAddr)
	if err := http.ListenAndServe(listenAddr, nil); err != nil {
		log.Fatalf("服务器启动失败: %v", err)
	}
}

func handleCreateSession(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost && r.Method != http.MethodGet {
		http.Error(w, "仅支持 GET/POST", http.StatusMethodNotAllowed)
		return
	}

	sess, err := createLoginSession()
	if err != nil {
		http.Error(w, fmt.Sprintf("创建扫码会话失败: %v", err), http.StatusBadGateway)
		return
	}

	writeJSON(w, sess)
}

func handleSessionStatus(w http.ResponseWriter, r *http.Request) {
	scene := strings.TrimPrefix(r.URL.Path, "/session/")
	if scene == "" {
		http.NotFound(w, r)
		return
	}

	value, ok := loginSessions.Load(scene)
	if !ok {
		http.NotFound(w, r)
		return
	}

	writeJSON(w, value)
}

func handleMessage(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	if !validateSignature(query) {
		http.Error(w, "签名校验失败", http.StatusForbidden)
		return
	}

	if r.Method == http.MethodGet {
		_, _ = w.Write([]byte(query.Get("echostr")))
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "读取请求失败", http.StatusInternalServerError)
		return
	}
	defer r.Body.Close()

	var evt wechatEvent
	if err := xml.Unmarshal(body, &evt); err != nil {
		http.Error(w, "解析事件失败", http.StatusBadRequest)
		return
	}

	if err := processEvent(&evt); err != nil {
		log.Printf("处理事件失败: %v", err)
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte("success"))
}

func createLoginSession() (*loginSession, error) {
	scene, err := generateState()
	if err != nil {
		return nil, err
	}

	qr, err := requestQRCode(scene)
	if err != nil {
		return nil, err
	}

	session := &loginSession{
		Scene:     scene,
		QRCodeURL: fmt.Sprintf("https://mp.weixin.qq.com/cgi-bin/showqrcode?ticket=%s", url.QueryEscape(qr.Ticket)),
		Status:    sessionStatusNew,
		CreatedAt: time.Now(),
		ExpiresAt: time.Now().Add(time.Duration(qr.ExpireSeconds) * time.Second),
	}
	loginSessions.Store(scene, session)
	return session, nil
}

func requestQRCode(scene string) (*qrCodeResponse, error) {
	token, err := getGlobalAccessToken()
	if err != nil {
		return nil, err
	}

	endpoint := fmt.Sprintf("https://api.weixin.qq.com/cgi-bin/qrcode/create?access_token=%s", url.QueryEscape(token))
	payload := map[string]interface{}{
		"expire_seconds": qrExpireSeconds,
		"action_name":    "QR_STR_SCENE",
		"action_info": map[string]interface{}{
			"scene": map[string]string{
				"scene_str": scene,
			},
		},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("微信接口返回状态码 %d", resp.StatusCode)
	}

	var qr qrCodeResponse
	if err := json.NewDecoder(resp.Body).Decode(&qr); err != nil {
		return nil, err
	}
	if qr.ErrCode != 0 {
		return nil, fmt.Errorf("微信返回错误: %d %s", qr.ErrCode, qr.ErrMsg)
	}
	return &qr, nil
}

func processEvent(evt *wechatEvent) error {
	if strings.ToLower(evt.MsgType) != "event" {
		return nil
	}
	eventType := strings.ToLower(strings.TrimSpace(evt.Event))
	if eventType != "subscribe" && eventType != "scan" {
		return nil
	}

	scene := normalizeScene(evt.EventKey)
	if scene == "" {
		return errors.New("缺少 scene，无法匹配会话")
	}

	value, ok := loginSessions.Load(scene)
	if !ok {
		return fmt.Errorf("scene=%s 未找到会话", scene)
	}

	session, ok := value.(*loginSession)
	if !ok {
		loginSessions.Delete(scene)
		return fmt.Errorf("会话数据异常: %s", scene)
	}
	user, err := fetchSubscribeInfo(evt.FromUserName)
	if err != nil {
		return err
	}
	if user.Subscribe != 1 {
		return fmt.Errorf("用户尚未关注")
	}

	session.Status = sessionStatusOK
	session.Scene = user.remark
	session.OpenID = evt.FromUserName
	loginSessions.Store(scene, session)
	return nil
}

func normalizeScene(eventKey string) string {
	if eventKey == "" {
		return ""
	}
	return strings.TrimPrefix(eventKey, "qrscene_")
}

func fetchSubscribeInfo(openID string) (*SubResponse, error) {
	token, err := getGlobalAccessToken()
	if err != nil {
		return nil, err
	}

	infoURL := fmt.Sprintf(
		"https://api.weixin.qq.com/cgi-bin/user/info?access_token=%s&openid=%s&lang=zh_CN",
		url.QueryEscape(token),
		url.QueryEscape(openID),
	)

	var resp SubResponse
	if err := getJSON(infoURL, &resp); err != nil {
		return nil, err
	}
	if resp.ErrCode != 0 {
		return nil, fmt.Errorf("微信返回错误: %d %s", resp.ErrCode, resp.ErrMsg)
	}
	return &resp, nil
}

func validateSignature(values url.Values) bool {
	signature := values.Get("signature")
	timestamp := values.Get("timestamp")
	nonce := values.Get("nonce")
	if signature == "" || timestamp == "" || nonce == "" {
		return false
	}

	parts := []string{token, timestamp, nonce}
	sort.Strings(parts)

	h := sha1.Sum([]byte(strings.Join(parts, "")))
	return signature == hex.EncodeToString(h[:])
}

func getGlobalAccessToken() (string, error) {
	globalTokenMu.RLock()
	if tokenValid(globalToken, globalTokenExpires) {
		defer globalTokenMu.RUnlock()
		return globalToken, nil
	}
	globalTokenMu.RUnlock()

	globalTokenMu.Lock()
	defer globalTokenMu.Unlock()

	if tokenValid(globalToken, globalTokenExpires) {
		return globalToken, nil
	}

	tokenValue, expiresIn, err := fetchGlobalAccessToken()
	if err != nil {
		return "", err
	}
	globalToken = tokenValue
	globalTokenExpires = time.Now().Add(time.Duration(expiresIn-60) * time.Second)
	return globalToken, nil
}

func tokenValid(token string, expiry time.Time) bool {
	return token != "" && time.Now().Before(expiry)
}

func fetchGlobalAccessToken() (string, int, error) {
	tokenURL := fmt.Sprintf(
		"https://api.weixin.qq.com/cgi-bin/token?grant_type=client_credential&appid=%s&secret=%s",
		url.QueryEscape(appID),
		url.QueryEscape(appSecret),
	)

	var resp globalTokenResponse
	if err := getJSON(tokenURL, &resp); err != nil {
		return "", 0, err
	}
	if resp.ErrCode != 0 {
		return "", 0, fmt.Errorf("微信返回错误: %d %s", resp.ErrCode, resp.ErrMsg)
	}
	return resp.AccessToken, resp.ExpiresIn, nil
}

func getJSON(endpoint string, dst interface{}) error {
	resp, err := httpClient.Get(endpoint)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("微信接口返回状态码 %d", resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(dst)
}

func generateState() (string, error) {
	buf := make([]byte, stateLength)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func cleanupExpiredSessions() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		now := time.Now()
		loginSessions.Range(func(key, value interface{}) bool {
			session, ok := value.(*loginSession)
			if !ok {
				loginSessions.Delete(key)
				return true
			}
			if now.After(session.ExpiresAt.Add(5 * time.Minute)) {
				loginSessions.Delete(key)
			}
			return true
		})
	}
}

func mustStatic() http.FileSystem {
	sub, err := fs.Sub(staticFiles, "static")
	if err != nil {
		panic(err)
	}
	return http.FS(sub)
}

type wechatEvent struct {
	XMLName      xml.Name `xml:"xml"`
	ToUserName   string   `xml:"ToUserName"`
	FromUserName string   `xml:"FromUserName"`
	CreateTime   int64    `xml:"CreateTime"`
	MsgType      string   `xml:"MsgType"`
	Event        string   `xml:"Event"`
	EventKey     string   `xml:"EventKey"`
	Ticket       string   `xml:"Ticket"`
}

type qrCodeResponse struct {
	Ticket        string `json:"ticket"`
	ExpireSeconds int    `json:"expire_seconds"`
	URL           string `json:"url"`
	ErrCode       int    `json:"errcode"`
	ErrMsg        string `json:"errmsg"`
}

type SubResponse struct {
	Subscribe int    `json:"subscribe"`
	OpenID    string `json:"openid"`
	remark    string `json:"remark"`
	ErrCode   int    `json:"errcode"`
	ErrMsg    string `json:"errmsg"`
}

type globalTokenResponse struct {
	AccessToken string `json:"access_token"`
	ExpiresIn   int    `json:"expires_in"`
	ErrCode     int    `json:"errcode"`
	ErrMsg      string `json:"errmsg"`
}

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		http.Error(w, fmt.Sprintf("编码 JSON 失败: %v", err), http.StatusInternalServerError)
	}
}
