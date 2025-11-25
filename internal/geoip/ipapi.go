package geoip

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"time"
)

// IPAPIService 使用 ip-api.com 服务实现 GeoIPService 接口。
type IPAPIService struct {
	Client *http.Client
}

// ipAPIResponse 定义了 ip-api.com 服务返回的 JSON 响应的结构。
type ipAPIResponse struct {
	Status      string  `json:"status"`
	Message     string  `json:"message"` // 当 status 为 fail 时出现
	Country     string  `json:"country"`
	CountryCode string  `json:"countryCode"`
	Region      string  `json:"region"`
	RegionName  string  `json:"regionName"`
	City        string  `json:"city"`
	Zip         string  `json:"zip"`
	Lat         float64 `json:"lat"`
	Lon         float64 `json:"lon"`
	Timezone    string  `json:"timezone"`
	ISP         string  `json:"isp"`
	Org         string  `json:"org"`
	As          string  `json:"as"`
	Query       string  `json:"query"`
}

func (s *IPAPIService) Name() string {
	return "ip-api.com"
}

// NewIPAPIService 创建并返回一个 IPAPIService 的新实例。
func NewIPAPIService() (*IPAPIService, error) {
	return &IPAPIService{
		Client: &http.Client{
			Timeout: 5 * time.Second, // 设置请求超时
		},
	}, nil
}

// GetGeoInfo 使用 ip-api.com 服务检索给定 IP 地址的地理位置信息。
func (s *IPAPIService) GetGeoInfo(ip net.IP) (*GeoInfo, error) {
	// API URL, 使用 fields 参数来仅请求需要的字段
	apiURL := fmt.Sprintf("http://ip-api.com/json/%s?fields=status,message,country,countryCode", ip.String())

	resp, err := s.Client.Get(apiURL)
	if err != nil {
		return nil, fmt.Errorf("failed to get geo info from ip-api.com: %w", err)
	}
	defer resp.Body.Close()

	var apiResp ipAPIResponse
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return nil, fmt.Errorf("failed to decode ip-api.com response: %w", err)
	}

	if apiResp.Status != "success" {
		return nil, fmt.Errorf("ip-api.com returned an error: %s", apiResp.Message)
	}

	return &GeoInfo{
		ISOCode: apiResp.CountryCode,
		Name:    apiResp.Country,
	}, nil
}

// UpdateDatabase 对于 ip-api.com 是一个空操作，因为它是一个 Web 服务。
func (s *IPAPIService) UpdateDatabase() error {
	// 无需执行任何操作，因为数据由外部服务提供
	return nil
}

// Close 对于 ip-api.com 是一个空操作，因为没有需要关闭的持久连接。
func (s *IPAPIService) Close() error {
	// 无需执行任何操作
	return nil
}
