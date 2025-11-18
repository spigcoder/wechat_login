package main

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"sync"
	"time"
)

const (
	stateCookieName = "wechat_service_login_state"
	stateLength     = 24
)

var (
	appID              = os.Getenv("WECHAT_SERVICE_APP_ID")
	appSecret          = os.Getenv("WECHAT_SERVICE_APP_SECRET")
	callbackURL        = os.Getenv("WECHAT_SERVICE_CALLBACK_URL")
	listenAddr         = ":" + envOrDefault("PORT", "8080")
	httpClient         = &http.Client{Timeout: 10 * time.Second}
	errMissingEnv      = errors.New("请设置 WECHAT_SERVICE_APP_ID、WECHAT_SERVICE_APP_SECRET 与 WECHAT_SERVICE_CALLBACK_URL 环境变量")
	globalTokenMu      sync.RWMutex
	globalToken        string
	globalTokenExpires time.Time
)

func main() {
	if appID == "" || appSecret == "" || callbackURL == "" {
		log.Fatal(errMissingEnv)
	}

	http.HandleFunc("/", handleLoginRedirect)
	http.HandleFunc("/wechat/callback", handleCallback)

	log.Printf("微信服务号关注登录示例已启动，监听 %s", listenAddr)
	if err := http.ListenAndServe(listenAddr, nil); err != nil {
		log.Fatalf("服务器启动失败: %v", err)
	}
}

func handleLoginRedirect(w http.ResponseWriter, r *http.Request) {
	state, err := generateState()
	if err != nil {
		http.Error(w, "生成状态失败", http.StatusInternalServerError)
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     stateCookieName,
		Value:    state,
		Path:     "/",
		HttpOnly: true,
		Secure:   r.TLS != nil,
		MaxAge:   300,
	})

	authURL := fmt.Sprintf(
		"https://open.weixin.qq.com/connect/oauth2/authorize?appid=%s&redirect_uri=%s&response_type=code&scope=snsapi_userinfo&state=%s#wechat_redirect",
		url.QueryEscape(appID),
		url.QueryEscape(callbackURL),
		url.QueryEscape(state),
	)

	http.Redirect(w, r, authURL, http.StatusFound)
}

func handleCallback(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	code := query.Get("code")
	state := query.Get("state")
	if code == "" || state == "" {
		http.Error(w, "缺少必要参数", http.StatusBadRequest)
		return
	}

	if !validateState(r, state) {
		http.Error(w, "state 无效", http.StatusBadRequest)
		return
	}

	token, err := fetchOAuthAccessToken(code)
	if err != nil {
		http.Error(w, fmt.Sprintf("获取 access_token 失败: %v", err), http.StatusBadGateway)
		return
	}

	user, err := fetchOAuthUser(token)
	if err != nil {
		http.Error(w, fmt.Sprintf("获取用户信息失败: %v", err), http.StatusBadGateway)
		return
	}

	subscribed, err := ensureSubscription(user.OpenID)
	if err != nil {
		http.Error(w, fmt.Sprintf("查询关注状态失败: %v", err), http.StatusBadGateway)
		return
	}
	if !subscribed {
		http.Error(w, "请先关注服务号后再登录", http.StatusForbidden)
		return
	}

	response := fmt.Sprintf("hello_service，欢迎 %s (openid: %s)", user.Nickname, user.OpenID)
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte(response))
}

func fetchOAuthAccessToken(code string) (*oauthTokenResponse, error) {
	tokenURL := fmt.Sprintf(
		"https://api.weixin.qq.com/sns/oauth2/access_token?appid=%s&secret=%s&code=%s&grant_type=authorization_code",
		url.QueryEscape(appID),
		url.QueryEscape(appSecret),
		url.QueryEscape(code),
	)

	var resp oauthTokenResponse
	if err := getJSON(tokenURL, &resp); err != nil {
		return nil, err
	}
	if resp.ErrCode != 0 {
		return nil, fmt.Errorf("微信返回错误: %d %s", resp.ErrCode, resp.ErrMsg)
	}
	return &resp, nil
}

func fetchOAuthUser(token *oauthTokenResponse) (*oauthUserResponse, error) {
	userURL := fmt.Sprintf(
		"https://api.weixin.qq.com/sns/userinfo?access_token=%s&openid=%s&lang=zh_CN",
		url.QueryEscape(token.AccessToken),
		url.QueryEscape(token.OpenID),
	)

	var user oauthUserResponse
	if err := getJSON(userURL, &user); err != nil {
		return nil, err
	}
	if user.ErrCode != 0 {
		return nil, fmt.Errorf("微信返回错误: %d %s", user.ErrCode, user.ErrMsg)
	}
	return &user, nil
}

func ensureSubscription(openID string) (bool, error) {
	token, err := getGlobalAccessToken()
	if err != nil {
		return false, err
	}

	infoURL := fmt.Sprintf(
		"https://api.weixin.qq.com/cgi-bin/user/info?access_token=%s&openid=%s&lang=zh_CN",
		url.QueryEscape(token),
		url.QueryEscape(openID),
	)

	var resp subscribeResponse
	if err := getJSON(infoURL, &resp); err != nil {
		return false, err
	}
	if resp.ErrCode != 0 {
		return false, fmt.Errorf("微信返回错误: %d %s", resp.ErrCode, resp.ErrMsg)
	}
	return resp.Subscribe == 1, nil
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

	token, expiresIn, err := fetchGlobalAccessToken()
	if err != nil {
		return "", err
	}
	globalToken = token
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

func validateState(r *http.Request, state string) bool {
	cookie, err := r.Cookie(stateCookieName)
	return err == nil && cookie.Value == state
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

type oauthTokenResponse struct {
	AccessToken  string `json:"access_token"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
	OpenID       string `json:"openid"`
	Scope        string `json:"scope"`
	ErrCode      int    `json:"errcode"`
	ErrMsg       string `json:"errmsg"`
}

type oauthUserResponse struct {
	OpenID     string   `json:"openid"`
	Nickname   string   `json:"nickname"`
	Sex        int      `json:"sex"`
	Province   string   `json:"province"`
	City       string   `json:"city"`
	Country    string   `json:"country"`
	HeadImgURL string   `json:"headimgurl"`
	Privilege  []string `json:"privilege"`
	UnionID    string   `json:"unionid"`
	ErrCode    int      `json:"errcode"`
	ErrMsg     string   `json:"errmsg"`
}

type subscribeResponse struct {
	Subscribe   int    `json:"subscribe"`
	OpenID      string `json:"openid"`
	Nickname    string `json:"nickname"`
	Sex         int    `json:"sex"`
	City        string `json:"city"`
	Country     string `json:"country"`
	Province    string `json:"province"`
	Language    string `json:"language"`
	HeadImgURL  string `json:"headimgurl"`
	SubscribeAt int64  `json:"subscribe_time"`
	UnionID     string `json:"unionid"`
	Remark      string `json:"remark"`
	GroupID     int    `json:"groupid"`
	TagIDList   []int  `json:"tagid_list"`
	SubscribeSC int    `json:"subscribe_scene"`
	ErrCode     int    `json:"errcode"`
	ErrMsg      string `json:"errmsg"`
}

type globalTokenResponse struct {
	AccessToken string `json:"access_token"`
	ExpiresIn   int    `json:"expires_in"`
	ErrCode     int    `json:"errcode"`
	ErrMsg      string `json:"errmsg"`
}
