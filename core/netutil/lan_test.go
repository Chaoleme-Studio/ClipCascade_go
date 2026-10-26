package netutil

import (
	"net"
	"testing"
)

func TestLANAddressesDoNotExcludeRealPrivateSubnets(t *testing.T) {
	for _, address := range []string{"192.168.52.1", "192.168.56.12", "192.168.64.5", "172.18.3.4", "10.0.0.1"} {
		if !IsUsableLANIPv4(net.ParseIP(address)) {
			t.Errorf("valid LAN address excluded: %s", address)
		}
	}
	for _, address := range []string{"198.18.0.1", "198.19.0.1", "127.0.0.1", "169.254.1.2", "224.0.0.251"} {
		if IsUsableLANIPv4(net.ParseIP(address)) {
			t.Errorf("non-LAN address included: %s", address)
		}
	}
}

func TestExcludeProxyAndContainerInterfaces(t *testing.T) {
	for _, name := range []string{"mihomo", "clash0", "br-123", "veth123", "virbr0", "podman0", "cni0"} {
		if !isVirtualInterfaceName(name) {
			t.Errorf("virtual interface included: %s", name)
		}
	}
	for _, name := range []string{"wlp0s20f3", "enp0s31f6", "eth0", "en0"} {
		if isVirtualInterfaceName(name) {
			t.Errorf("physical interface excluded: %s", name)
		}
	}
}
