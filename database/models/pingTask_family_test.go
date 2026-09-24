package models

import "testing"

func TestPingTaskFamilyCompatibility(t *testing.T) {
	legacy := PingTask{}
	if !legacy.Active() || !legacy.HasFamily("ipv4") || legacy.HasFamily("ipv6") {
		t.Fatal("legacy task should remain IPv4-only")
	}
	v6Only := PingTask{IPFamilies: StringArray{"ipv6"}}
	if v6Only.Active() || v6Only.HasFamily("ipv4") || !v6Only.HasFamily("ipv6") {
		t.Fatal("IPv6-only parent must not run an IPv4 probe")
	}
	child := PingTask{ParentID: 1, Family: "ipv6", Enabled: true}
	if !child.Active() || !child.HasFamily("ipv6") {
		t.Fatal("enabled IPv6 child must run")
	}
	child.Enabled = false
	if child.Active() {
		t.Fatal("disabled IPv6 child must not run")
	}
}
