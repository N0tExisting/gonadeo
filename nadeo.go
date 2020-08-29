package nadeo

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"time"

	"github.com/patrickmn/go-cache"
)

// Nadeo provides access to the Nadeo Live Services API.
type Nadeo interface {
	AuthenticateUbi(email, password string) error
	AuthenticateUbiTicket(ticket string) error
	Authenticate(username, password string) error
	GetTokenInfo() TokenInfo

	Get(url string, useCache bool) (string, error)
	Post(url, data string) (string, error)

	CheckRefresh() error
}

type nadeo struct {
	audience string

	accessToken  string
	refreshToken string

	tokenRefreshTime    uint32
	tokenExpirationTime uint32

	requestCache *cache.Cache
}

func (n *nadeo) AuthenticateUbi(email, password string) error {
	ubi := NewUbi("86263886-327a-4328-ac69-527f0d20a237")
	ubi.Authenticate(email, password)
	return n.AuthenticateUbiTicket(ubi.GetTicket())
}

func (n *nadeo) AuthenticateUbiTicket(ticket string) error {
	body := bytes.NewReader([]byte("{\"audience\":\"" + n.audience + "\"}"))

	req, err := http.NewRequest("POST", "https://prod.trackmania.core.nadeo.online/v2/authentication/token/ubiservices", body)
	if err != nil {
		return fmt.Errorf("unable to make request: %s", err.Error())
	}

	req.Header.Add("Content-Type", "application/json")
	req.Header.Add("Authorization", "ubi_v1 t="+ticket)

	client := http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("unable to perform request: %s", err.Error())
	}

	resBytes := make([]byte, resp.ContentLength)
	io.ReadFull(resp.Body, resBytes)

	if resp.StatusCode != 200 {
		respError := errorResponse{}
		json.Unmarshal(resBytes, &respError)
		return fmt.Errorf("error %d from server: %s", respError.Code, respError.Message)
	}

	res := authResponse{}
	json.Unmarshal(resBytes, &res)

	n.accessToken = res.AccessToken
	n.refreshToken = res.RefreshToken

	tokenInfo := parseTokenInfo(n.accessToken)
	n.tokenRefreshTime = tokenInfo.Payload.Rat
	n.tokenExpirationTime = tokenInfo.Payload.Exp

	return nil
}

func (n *nadeo) Authenticate(username, password string) error {
	body := bytes.NewReader([]byte("{\"audience\":\"" + n.audience + "\"}"))

	req, err := http.NewRequest("POST", "https://prod.trackmania.core.nadeo.online/v2/authentication/token/basic", body)
	if err != nil {
		return fmt.Errorf("unable to make request: %s", err.Error())
	}

	req.Header.Add("Content-Type", "application/json")
	req.SetBasicAuth(username, password)

	client := http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("unable to perform request: %s", err.Error())
	}

	resBytes := make([]byte, resp.ContentLength)
	io.ReadFull(resp.Body, resBytes)

	if resp.StatusCode != 200 {
		respError := errorResponse{}
		json.Unmarshal(resBytes, &respError)
		// 401: "Username could not be found."  -> Invalid username
		// 401: "Invalid credentials."          -> Invalid password
		//   0: "There was a validation error." -> Invalid audience
		return fmt.Errorf("error %d from server: %s", respError.Code, respError.Message)
	}

	res := authResponse{}
	json.Unmarshal(resBytes, &res)

	n.accessToken = res.AccessToken
	n.refreshToken = res.RefreshToken

	tokenInfo := parseTokenInfo(n.accessToken)
	n.tokenRefreshTime = tokenInfo.Payload.Rat
	n.tokenExpirationTime = tokenInfo.Payload.Exp

	return nil
}

func (n *nadeo) GetTokenInfo() TokenInfo {
	return parseTokenInfo(n.accessToken)
}

func (n *nadeo) Get(url string, useCache bool) (string, error) {
	return n.request("GET", url, useCache, "")
}

func (n *nadeo) Post(url, data string) (string, error) {
	return n.request("POST", url, false, data)
}

func (n *nadeo) CheckRefresh() error {
	now := uint32(time.Now().Unix())
	if now > n.tokenRefreshTime {
		err := n.refreshNow()
		if err != nil {
			return fmt.Errorf("unable to refresh token: %s", err.Error())
		}
	}
	return nil
}

func (n *nadeo) request(method string, url string, useCache bool, data string) (string, error) {
	if useCache {
		cachedResponse, cacheFound := n.requestCache.Get(url)
		if cacheFound {
			return cachedResponse.(string), nil
		}
	}

	err := n.CheckRefresh()
	if err != nil {
		return "", err
	}

	var body io.Reader
	if method == "POST" {
		body = bytes.NewReader([]byte(data))
	}

	req, err := http.NewRequest(method, url, body)
	if err != nil {
		return "", fmt.Errorf("unable to make request: %s", err.Error())
	}

	req.Header.Add("Authorization", "nadeo_v1 t="+n.accessToken)
	req.Header.Add("Accept", "application/json")
	req.Header.Add("Content-Type", "application/json")

	client := http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("unable to perform request: %s", err.Error())
	}

	resBytes, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("unable to read from stream: %s", err.Error())
	}

	if resp.StatusCode != 200 {
		//respError := errorResponse{}
		//err := json.Unmarshal(resBytes, &respError)
		return "", fmt.Errorf("error from server: %s", string(resBytes))
		//return "", fmt.Errorf("error %d from server: %s", respError.Code, respError.Message)
	}

	if useCache {
		n.requestCache.Set(url, string(resBytes), cache.DefaultExpiration)
	}

	return string(resBytes), nil
}

func (n *nadeo) refreshNow() error {
	req, err := http.NewRequest("POST", "https://prod.trackmania.core.nadeo.online/v2/authentication/token/refresh", nil)
	if err != nil {
		return fmt.Errorf("unable to make request: %s", err.Error())
	}

	req.Header.Add("Authorization", "nadeo_v1 t="+n.refreshToken)

	client := http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("unable to perform request: %s", err.Error())
	}

	resBytes := make([]byte, resp.ContentLength)
	io.ReadFull(resp.Body, resBytes)

	if resp.StatusCode != 200 {
		respError := errorResponse{}
		json.Unmarshal(resBytes, &respError)
		return fmt.Errorf("error %d from server: %s", respError.Code, respError.Message)
	}

	res := authResponse{}
	json.Unmarshal(resBytes, &res)

	n.accessToken = res.AccessToken
	n.refreshToken = res.RefreshToken

	tokenInfo := parseTokenInfo(n.accessToken)
	n.tokenRefreshTime = tokenInfo.Payload.Rat
	n.tokenExpirationTime = tokenInfo.Payload.Exp

	return nil
}

// NewNadeo creates a new Nadeo object ready for authentication.
func NewNadeo() Nadeo {
	return NewNadeoWithAudience("NadeoLiveServices")
}

// NewNadeoWithAudience creates a new Nadeo object ready for authentication with the given audience.
func NewNadeoWithAudience(audience string) Nadeo {
	return &nadeo{
		audience:     audience,
		requestCache: cache.New(1*time.Minute, 5*time.Minute),
	}
}
