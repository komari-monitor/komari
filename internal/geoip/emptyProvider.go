package geoip

import (
	"fmt"
	"net"
)

type EmptyProvider struct{}

func (e *EmptyProvider) Name() string {
	return "EmptyProvider"
}

func (e *EmptyProvider) Initialize() error {
	return nil
}
func (e *EmptyProvider) GetGeoInfo(ip net.IP) (*GeoInfo, error) {
	return nil, fmt.Errorf("you are using an empty GeoIP provider, please set a valid provider")
}
func (e *EmptyProvider) UpdateDatabase() error {
	return fmt.Errorf("you are using an empty GeoIP provider, please set a valid provider")
}
func (e *EmptyProvider) Close() error {
	return nil
}
