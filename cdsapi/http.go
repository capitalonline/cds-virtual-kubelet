package cdsapi

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"github.com/virtual-kubelet/virtual-kubelet/log"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"
)

type CloudRequest struct {
	method      string
	params      map[string]string
	action      string
	productType string
	body        interface{}
}

func NewCCKRequest(ctx context.Context, action, method string, params map[string]string, body interface{}) (*CloudRequest, error) {
	if params == nil {
		params = make(map[string]string)
	}
	if customerId := os.Getenv("CUSTOMER_ID"); customerId != "" {
		params["CustomerId"] = customerId
	}
	if userId := os.Getenv("USER_ID"); userId != "" {
		params["UserId"] = userId
	}
	return NewRequest(action, method, params, cckProductType, body), nil
}

func NewRequest(action, method string, params map[string]string, productType string, body interface{}) *CloudRequest {
	method = strings.ToUpper(method)
	req := &CloudRequest{
		method:      method,
		params:      params,
		action:      action,
		productType: productType,
		body:        body,
	}
	return req
}

func DoOpenApiRequest(ctx context.Context, req *CloudRequest, staggered int) (resp *http.Response, err error) {
	if !IsAccessKeySet() {
		return nil, fmt.Errorf("AccessKeyID or accessKeySecret is empty")
	}
	if staggered != 0 {
		Staggered(staggered)
	}
	for i := 0; i < 3; i++ {
		reqUrl := getUrl(req)
		b, _ := json.Marshal(req.body)
		log.G(ctx).WithField("Action", req.action).Debug(string(b))
		resp, err = DoRequest(req.method, reqUrl, bytes.NewReader(b))
		if err != nil || resp.StatusCode >= 500 {
			log.G(ctx).WithField("CDS", "cds-debug").Debug(fmt.Sprintf("post %s 50x", req.action))
			time.Sleep(10 * time.Second)
			continue
		} else {
			break
		}
	}
	return
}

func DoRequest(method, url string, body io.Reader) (resp *http.Response, err error) {
	sendRequest, err := http.NewRequest(method, url, body)
	if err != nil {
		return
	}
	resp, err = http.DefaultClient.Do(sendRequest)
	return
}

func getUrl(req *CloudRequest) string {
	urlParams := map[string]string{
		"Action":           req.action,
		"AccessKeyId":      AccessKeyID,
		"SignatureMethod":  signatureMethod,
		"SignatureNonce":   uuid.New().String(),
		"SignatureVersion": signatureVersion,
		"Timestamp":        time.Now().UTC().Format(timeStampFormat),
		"Version":          version,
	}
	if req.params != nil {
		for k, v := range req.params {
			urlParams[k] = v
		}
	}
	var paramSortKeys sort.StringSlice
	for k, _ := range urlParams {
		paramSortKeys = append(paramSortKeys, k)
	}
	sort.Sort(paramSortKeys)
	var urlStr string
	for _, k := range paramSortKeys {
		urlStr += "&" + percentEncode(k) + "=" + percentEncode(urlParams[k])
	}
	urlStr = req.method + "&%2F&" + percentEncode(urlStr[1:])

	h := hmac.New(sha1.New, []byte(AccessKeySecret))
	h.Write([]byte(urlStr))
	signStr := base64.StdEncoding.EncodeToString(h.Sum(nil))

	urlParams["Signature"] = signStr

	urlVal := url.Values{}
	for k, v := range urlParams {
		urlVal.Add(k, v)
	}
	urlValStr := urlVal.Encode()
	reqUrl := fmt.Sprintf("%s/%s?%s", APIHost, req.productType, urlValStr)
	return reqUrl
}

func percentEncode(str string) string {
	str = url.QueryEscape(str)
	strings.Replace(str, "+", "%20", -1)
	strings.Replace(str, "*", "%2A", -1)
	strings.Replace(str, "%7E", "~", -1)
	return str
}

type Response struct {
	Code     string      `json:"Code"`
	Message  string      `json:"Message"`
	CodeDesc string      `json:"codeDesc,omitempty"`
	Data     interface{} `json:"Data"`
}

func CdsRespDeal(ctx context.Context, response *http.Response, data interface{}) (string, string, error) {
	content, err := io.ReadAll(response.Body)
	if err != nil {
		return "", error.Error(err), err
	}
	log.G(ctx).WithField("Func", "CdsResp").Debug(string(content))
	if response.StatusCode >= 400 {
		return "", "", fmt.Errorf("response code: %v", response.StatusCode)
	}
	var res Response
	err = json.Unmarshal(content, &res)
	if err != nil {
		return "", error.Error(err), err
	}
	b, err := json.Marshal(res.Data)
	if err != nil {
		return "", error.Error(err), err
	}
	if data != nil {
		err = json.Unmarshal(b, data)
		if err != nil {
			return "", error.Error(err), err
		}
	}
	return res.Code, res.Message, nil
}
