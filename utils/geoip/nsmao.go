package geoip

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"time"
)

// NsmaoService 使用 api.nsmao.net 服务实现 GeoIPService 接口。
type NsmaoService struct {
	Client *http.Client
	APIKey string
}

// nsmaoResponse 定义了 api.nsmao.net 服务返回的 JSON 响应结构。
type nsmaoResponse struct {
	Code    int    `json:"code"`
	Msg     string `json:"msg"`
	Data    struct {
		IP      string `json:"ip"`
		Country string `json:"country"`
	} `json:"data"`
	IP string `json:"ip"`
}

// NewNsmaoService 创建并返回一个 NsmaoService 实例。
func NewNsmaoService() (*NsmaoService, error) {
	return &NsmaoService{
		Client: &http.Client{
			Timeout: 5 * time.Second,
		},
		APIKey: "A6x59bUBvROr2Saciv1IJ68FZA", // 默认API密钥
	}, nil
}

// Name 返回服务的名称。
func (s *NsmaoService) Name() string {
	return "nsmao"
}

// GetGeoInfo 使用 nsmao 服务检索给定 IP 地址的地理位置信息。
func (s *NsmaoService) GetGeoInfo(ip net.IP) (*GeoInfo, error) {
	// API URL
	apiURL := fmt.Sprintf("https://api.nsmao.net/api/ipip/query?key=%s&ip=%s", s.APIKey, ip.String())

	resp, err := s.Client.Get(apiURL)
	if err != nil {
		return nil, fmt.Errorf("failed to get geo info from nsmao: %w", err)
	}
	defer resp.Body.Close()

	// 检查响应状态码
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("nsmao returned non-200 status code: %d", resp.StatusCode)
	}

	var apiResp nsmaoResponse
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return nil, fmt.Errorf("failed to decode nsmao response: %w", err)
	}

	// 检查API响应码
	if apiResp.Code != 200 {
		return nil, fmt.Errorf("nsmao API returned error: %s", apiResp.Msg)
	}

	// 注意：这个API只提供中文国家名，没有ISO代码
	// 我们暂时将国家名同时作为ISOCode和Name，后续可能需要映射表来转换为标准ISO代码
	return &GeoInfo{
		ISOCode: apiResp.Data.Country,
		Name:    apiResp.Data.Country,
	}, nil
}

// UpdateDatabase 对于 nsmao 是一个空操作，因为它是一个 Web 服务。
func (s *NsmaoService) UpdateDatabase() error {
	return nil
}

// Close 对于 nsmao 是一个空操作。
func (s *NsmaoService) Close() error {
	return nil
}