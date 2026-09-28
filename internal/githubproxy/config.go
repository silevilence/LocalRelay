// Package githubproxy implements a read-only GitHub gateway, independent of LLM routing.
package githubproxy

import (
	"fmt"
	"net/url"
	"strconv"
)

type Config struct {
	Port         int    `json:"port"`
	Mode         string `json:"mode"`
	ProxyType    string `json:"proxyType"`
	ProxyAddress string `json:"proxyAddress"`
}

func DefaultConfig() Config { return Config{Port: 8719, Mode: "auto", ProxyType: "http"} }

func (c Config) Validate() error {
	if c.Port < 1 || c.Port > 65535 {
		return fmt.Errorf("监听端口必须为 1–65535")
	}
	if c.Mode != "auto" && c.Mode != "direct" && c.Mode != "proxy" {
		return fmt.Errorf("无效链路模式")
	}
	if c.ProxyType != "http" && c.ProxyType != "socks5" {
		return fmt.Errorf("代理类型必须为 HTTP 或 SOCKS5")
	}
	if c.ProxyAddress == "" {
		if c.Mode == "proxy" {
			return fmt.Errorf("仅代理模式需要填写代理地址")
		}
		return nil
	}
	u, err := url.Parse(c.ProxyType + "://" + c.ProxyAddress)
	if err != nil {
		return fmt.Errorf("代理地址应为 host:port")
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil || port < 1 || port > 65535 || u.Hostname() == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("代理地址应为 host:port（不含协议、凭据或路径）")
	}
	return nil
}
