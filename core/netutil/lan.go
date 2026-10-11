package netutil

import (
	"net"
	"net/url"
	"sort"
	"strings"
)

// IsUsableLANIPv4 reports whether ip is a good candidate for local network discovery.
func IsUsableLANIPv4(ip net.IP) bool {
	v4 := ip.To4()
	if v4 == nil {
		return false
	}
	if v4.IsLoopback() || v4.IsUnspecified() || v4.IsMulticast() || v4.IsLinkLocalUnicast() {
		return false
	}

	// Common host-only / virtual ranges that frequently appear before the real Wi-Fi IP.
	if v4[0] == 192 && v4[1] == 168 && (v4[2] == 52 || v4[2] == 56 || v4[2] == 64) {
		return false
	}
	// 修复记录：不再按网段排除 172.16/12 —— 企业内网常用 172.16-31.x.x，
	// 虚拟网卡（docker/vbox 等）已由 isVirtualInterfaceName 按接口名过滤。
	// CGNAT 100.64/10 仍排除：蜂窝数据与部分 VPN 出口使用该段，不适合局域网发现。
	if v4[0] == 100 && v4[1] >= 64 && v4[1] <= 127 {
		return false
	}
	return true
}

// IsUsableLANInterface reports whether iface should participate in mDNS publishing/browsing.
func IsUsableLANInterface(iface net.Interface) bool {
	if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagMulticast == 0 || iface.Flags&net.FlagLoopback != 0 {
		return false
	}
	name := strings.ToLower(iface.Name)
	if isVirtualInterfaceName(name) {
		return false
	}
	addrs, err := iface.Addrs()
	if err != nil {
		return false
	}
	for _, addr := range addrs {
		if ipNet, ok := addr.(*net.IPNet); ok && IsUsableLANIPv4(ipNet.IP) {
			return true
		}
	}
	return false
}

func isVirtualInterfaceName(name string) bool {
	if strings.HasPrefix(name, "bridge") ||
		strings.HasPrefix(name, "docker") ||
		strings.HasPrefix(name, "utun") ||
		strings.HasPrefix(name, "awdl") ||
		strings.HasPrefix(name, "llw") ||
		strings.HasPrefix(name, "vbox") ||
		strings.HasPrefix(name, "vmnet") ||
		strings.HasPrefix(name, "tap") ||
		strings.HasPrefix(name, "tun") ||
		strings.HasPrefix(name, "wg") ||
		strings.HasPrefix(name, "zt") {
		return true
	}
	return strings.Contains(name, "tailscale") || strings.Contains(name, "zerotier")
}

// UsableLANInterfaces returns physical-looking multicast interfaces for mDNS.
func UsableLANInterfaces() []net.Interface {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	out := make([]net.Interface, 0, len(ifaces))
	for _, iface := range ifaces {
		if IsUsableLANInterface(iface) {
			out = append(out, iface)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		return interfaceRank(out[i].Name) < interfaceRank(out[j].Name)
	})
	return out
}

// LocalIPv4s returns usable local IPv4 addresses in a stable, user-friendly order.
func LocalIPv4s() []string {
	var ips []string
	for _, iface := range UsableLANInterfaces() {
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			ipNet, ok := addr.(*net.IPNet)
			if !ok || !IsUsableLANIPv4(ipNet.IP) {
				continue
			}
			ips = append(ips, ipNet.IP.String())
		}
	}
	SortDiscoveredURLs(ips)
	return ips
}

// SortDiscoveredURLs puts real LAN-looking addresses before virtual or fallback addresses.
func SortDiscoveredURLs(values []string) {
	sort.SliceStable(values, func(i, j int) bool {
		return discoveryRank(values[i]) < discoveryRank(values[j])
	})
}

func discoveryRank(value string) int {
	host := value
	if parsed, err := netUrlHost(value); err == nil && parsed != "" {
		host = parsed
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	if ip == nil {
		return 50
	}
	if IsUsableLANIPv4(ip) {
		v4 := ip.To4()
		if v4[0] == 192 && v4[1] == 168 {
			return 0
		}
		if v4[0] == 10 {
			return 1
		}
		return 2
	}
	return 100
}

func interfaceRank(name string) int {
	n := strings.ToLower(name)
	if strings.HasPrefix(n, "en") || strings.HasPrefix(n, "wl") || strings.HasPrefix(n, "wi") {
		return 0
	}
	if strings.HasPrefix(n, "eth") {
		return 1
	}
	return 10
}

func netUrlHost(value string) (string, error) {
	if !strings.Contains(value, "://") {
		return value, nil
	}
	u, err := url.Parse(value)
	if err != nil {
		return "", err
	}
	return u.Hostname(), nil
}
