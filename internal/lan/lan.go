// Package lan enumerates the addresses a phone or another machine on the same
// network can use to reach this host.
package lan

import (
	"bytes"
	"net"
	"sort"
	"strings"
	"time"
)

// Address is one usable non-loopback IPv4 address of this host.
type Address struct {
	IP      net.IP
	Iface   string
	Private bool // RFC1918 / RFC6598: reachable inside the local network only
	Primary bool // the address the default route uses
}

// Addresses returns the addresses worth showing a user: every non-loopback,
// non-link-local IPv4 address of an interface that is not a container or VM
// bridge, primary route first, then private ranges, then the rest.
func Addresses() []Address {
	all := All()
	out := make([]Address, 0, len(all))
	for _, a := range all {
		if !isVirtualIface(a.Iface) {
			out = append(out, a)
		}
	}
	return out
}

// All returns every non-loopback, non-link-local IPv4 address, including
// container and VM bridges. It is what the Host allowlist is built from, since
// a browser may legitimately arrive through any of them.
func All() []Address {
	primary := primaryIPv4()

	var out []Address
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			ipnet, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			ip4 := ipnet.IP.To4()
			if ip4 == nil || ip4.IsLoopback() || ip4.IsLinkLocalUnicast() {
				continue
			}
			out = append(out, Address{
				IP:      ip4,
				Iface:   iface.Name,
				Private: isPrivate(ip4),
				Primary: primary != nil && primary.Equal(ip4),
			})
		}
	}

	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Primary != b.Primary {
			return a.Primary
		}
		if a.Private != b.Private {
			return a.Private
		}
		return bytes.Compare(a.IP, b.IP) < 0
	})
	return out
}

// primaryIPv4 resolves the source address of the default route. A UDP "dial"
// only consults the routing table and sends no packet, so this works offline
// as long as the host has a default route.
func primaryIPv4() net.IP {
	conn, err := net.DialTimeout("udp4", "1.1.1.1:53", 200*time.Millisecond)
	if err != nil {
		return nil
	}
	defer conn.Close()
	if addr, ok := conn.LocalAddr().(*net.UDPAddr); ok {
		return addr.IP.To4()
	}
	return nil
}

// isVirtualIface reports whether name looks like a container or VM bridge
// that no phone is going to dial.
func isVirtualIface(name string) bool {
	for _, prefix := range []string{"docker", "br-", "veth", "virbr", "vmnet", "podman", "cni", "flannel"} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

// isPrivate reports whether ip is in a range that is not routed on the public
// internet: RFC1918 plus carrier-grade NAT, which container and VPN bridges use.
func isPrivate(ip net.IP) bool {
	if ip.IsPrivate() {
		return true
	}
	_, cgnat, _ := net.ParseCIDR("100.64.0.0/10")
	return cgnat.Contains(ip)
}
