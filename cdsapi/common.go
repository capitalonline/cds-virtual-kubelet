package cdsapi

import (
	"fmt"
	log "github.com/sirupsen/logrus"
	"math/rand"
	"os"
	"time"
)

const (
	defaultApiHost = "http://cdsapi.capitalonline.net"
	// preApiHost             = "http://cdsapi-gateway.gic.pre/openapi"
	accessKeyIdLiteral     = "CDS_ACCESS_KEY_ID"
	accessKeySecretLiteral = "CDS_ACCESS_KEY_SECRET"
	cckProductType         = "cck"
	version                = "2019-08-08"
	signatureVersion       = "1.0"
	signatureMethod        = "HMAC-SHA1"
	timeStampFormat        = "2006-01-02T15:04:05Z"
)

var (
	APIHost         string
	AccessKeyID     string
	AccessKeySecret string
)

func IsAccessKeySet() bool {
	return AccessKeyID != "" && AccessKeySecret != ""
}

func init() {
	if AccessKeyID == "" {
		AccessKeyID = os.Getenv(accessKeyIdLiteral)
	}
	if AccessKeySecret == "" {
		AccessKeySecret = os.Getenv(accessKeySecretLiteral)
	}

	dnsDeal()
	_, _ = Run("sh", "-c", "echo '101.251.217.74  cdsapi-gateway.gic.pre' >> /etc/hosts")
	_, _ = Run("sh", "-c", "echo '10.2.10.101  openapi.gic.test' >> /etc/hosts")

	APIHost = os.Getenv("OPENAPI_HOST")

}

func dnsDeal() {
	dns := "nameserver 8.8.8.8"
	oversea := os.Getenv("CDS_OVERSEA")
	if oversea != "True" {
		dns = "nameserver 114.114.114.114"
	}
	_, err := Run("sh", "-c", "cp /etc/resolv.conf /etc/resolv.conf.bak")
	if err != nil {
		log.Warnf("cp /etc/resolv.conf /etc/resolv.conf.bak err: %v", err)
		return
	}

	sh := fmt.Sprintf("sed '1i\\%s' /etc/resolv.conf.bak > /etc/resolv.conf", dns)
	_, err = Run("sh", "-c", sh)
	if err != nil {
		log.Warnf("add nameserver err: %v", err)
		return
	}
}

func Staggered(t int) {
	rand.Seed(time.Now().UnixNano())
	n := rand.Intn(t)
	time.Sleep(time.Duration(n) * 1000 * 1000)
}
