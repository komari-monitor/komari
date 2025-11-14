package geoip

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"time"
)

// GeoJSService 使用 geojs.io 服务实现 GeoIPService 接口。
type GeoJSService struct {
	Client *http.Client
}

// geoJSResponse 定义了 geojs.io 服务返回的 JSON 响应的结构。
// 我们只定义我们需要的字段。
type geoJSResponse struct {
	Country     string `json:"country"`
	CountryCode string `json:"country_code"`
	// 可以根据需要添加其他字段，例如:
	// City    string `json:"city"`
	// Region  string `json:"region"`
}

// NewGeoJSService 创建并返回一个 GeoJSService 的新实例。
func NewGeoJSService() (*GeoJSService, error) {
	return &GeoJSService{
		Client: &http.Client{
			Timeout: 5 * time.Second, // 设置一个合理的超时时间
		},
	}, nil
}

// Name 返回服务的名称。
func (s *GeoJSService) Name() string {
	return "geojs.io"
}

// GetGeoInfo 使用 geojs.io 服务检索给定 IP 地址的地理位置信息。
func (s *GeoJSService) GetGeoInfo(ip net.IP) (*GeoInfo, error) {
	// GeoJS 的 API 端点
	apiURL := fmt.Sprintf("https://get.geojs.io/v1/ip/geo/%s.json", ip.String())

	resp, err := s.Client.Get(apiURL)
	if err != nil {
		return nil, fmt.Errorf("failed to get geo info from geojs.io: %w", err)
	}
	defer resp.Body.Close()

	// 检查响应状态码
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("geojs.io returned non-200 status code: %d", resp.StatusCode)
	}

	var apiResp geoJSResponse
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return nil, fmt.Errorf("failed to decode geojs.io response: %w", err)
	}

	// 检查国家代码是否为空，因为 geojs 对无效/私有IP可能返回200 OK但内容为空
	if apiResp.CountryCode == "" {
		return nil, fmt.Errorf("geojs.io returned empty geo info for ip: %s", ip.String())
	}

	return &GeoInfo{
		ISOCode: apiResp.CountryCode,
		Name:    apiResp.Country,
	}, nil
}

// UpdateDatabase 对于 geojs.io 是一个空操作，因为它是一个 Web 服务。
func (s *GeoJSService) UpdateDatabase() error {
	return nil
}

// Close 对于 geojs.io 是一个空操作。
func (s *GeoJSService) Close() error {
	return nil
}
