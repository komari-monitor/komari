package agent

import (
	"errors"
	"slices"
	"strings"

	v2 "github.com/komari-monitor/komari/protocol/v2"
)

var (
	ErrSwitchVersionInvalidParams = errors.New("agent uuid and version are required")
	ErrSwitchVersionOffline       = errors.New("agent is offline")
	ErrSwitchVersionUnsupported   = errors.New("agent does not support v2 version switching")
)

func SwitchAgentVersion(uuid, version string) error {
	uuid = strings.TrimSpace(uuid)
	version = strings.TrimSpace(version)
	if uuid == "" || version == "" {
		return ErrSwitchVersionInvalidParams
	}
	if !slices.Contains(GetAllOnlineUUIDs(), uuid) {
		return ErrSwitchVersionOffline
	}
	if !IsV2Client(uuid) {
		return ErrSwitchVersionUnsupported
	}
	if !DispatchV2Event(uuid, v2.MethodAgentSwitchVersion, v2.SwitchVersionParams{Version: version}) {
		return ErrSwitchVersionOffline
	}
	return nil
}
