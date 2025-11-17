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
	"time"
)

const (
	stateCookieName = "wechat_login_state"
	stateLength     = 24
)

var (
	appID         = os.Getenv("WECHAT_APP_ID")
	appSecret     = os.Getenv("WECHAT_APP_SECRET")
	callbackURL   = os.Getenv("WECHAT_CALLBACK_URL")
	listenAddr    = ":" + envOrDefault("PORT", "8080")
	httpClient    = &http.Client{Timeout: 10 * time.Second}
	errMissingEnv = errors.New("请设置 WECHAT_APP_ID、WECHAT_APP_SECRET 与 WECHAT_CALLBACK_URL 环境变量")
)

func main() {
	if appID == "" || appSecret == "" || callbackURL == "" {
		log.Fatal(errMissingEnv)
	}

	http.HandleFunc("/", handleLoginRedirect)
	http.HandleFunc("/wechat/callback", handleCallback)

	log.Printf("微信登录示例服务已启动，监听 %s", listenAddr)
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
		"https://open.weixin.qq.com/connect/qrconnect?appid=%s&redirect_uri=%s&response_type=code&scope=snsapi_login&state=%s#wechat_redirect",
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

	token, err := fetchAccessToken(code)
	if err != nil {
		http.Error(w, fmt.Sprintf("获取 access_token 失败: %v", err), http.StatusBadGateway)
		return
	}

	user, err := fetchUserInfo(token)
	if err != nil {
		http.Error(w, fmt.Sprintf("获取用户信息失败: %v", err), http.StatusBadGateway)
		return
	}

	response := fmt.Sprintf("hello_world，欢迎 %s (openid: %s)", user.Nickname, user.OpenID)
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte(response))
}

func fetchAccessToken(code string) (*tokenResponse, error) {
	tokenURL := fmt.Sprintf(
		"https://api.weixin.qq.com/sns/oauth2/access_token?appid=%s&secret=%s&code=%s&grant_type=authorization_code",
		url.QueryEscape(appID),
		url.QueryEscape(appSecret),
		url.QueryEscape(code),
	)

	var resp tokenResponse
	if err := getJSON(tokenURL, &resp); err != nil {
		return nil, err
	}
	if resp.ErrCode != 0 {
		return nil, fmt.Errorf("微信返回错误: %d %s", resp.ErrCode, resp.ErrMsg)
	}
	return &resp, nil
}

func fetchUserInfo(token *tokenResponse) (*userInfoResponse, error) {
	userURL := fmt.Sprintf(
		"https://api.weixin.qq.com/sns/userinfo?access_token=%s&openid=%s&lang=zh_CN",
		url.QueryEscape(token.AccessToken),
		url.QueryEscape(token.OpenID),
	)

	var user userInfoResponse
	if err := getJSON(userURL, &user); err != nil {
		return nil, err
	}
	if user.ErrCode != 0 {
		return nil, fmt.Errorf("微信返回错误: %d %s", user.ErrCode, user.ErrMsg)
	}
	return &user, nil
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

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
	OpenID       string `json:"openid"`
	Scope        string `json:"scope"`
	ErrCode      int    `json:"errcode"`
	ErrMsg       string `json:"errmsg"`
}

type userInfoResponse struct {
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
