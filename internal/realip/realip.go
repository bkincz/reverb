package realip

import (
	"net"
	"net/http"
	"strings"
)

type Resolver struct {
	trusted []*net.IPNet
}

func New(trusted []string) (*Resolver, error) {
	nets := make([]*net.IPNet, 0, len(trusted))
	for _, raw := range trusted {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}

		if _, network, err := net.ParseCIDR(raw); err == nil {
			nets = append(nets, network)
			continue
		}

		ip := net.ParseIP(raw)
		if ip == nil {
			return nil, &net.ParseError{Type: "IP address or CIDR", Text: raw}
		}
		nets = append(nets, ipNet(ip))
	}

	return &Resolver{trusted: nets}, nil
}

func RemoteAddr(r *http.Request) string {
	if ip := parseIP(r.RemoteAddr); ip != nil {
		return ip.String()
	}

	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	return r.RemoteAddr
}

func (r *Resolver) ClientIP(req *http.Request) string {
	remote := parseIP(req.RemoteAddr)
	if remote == nil {
		return RemoteAddr(req)
	}
	if !r.isTrusted(remote) {
		return remote.String()
	}

	if chain := forwardedIPs(req.Header.Get("X-Forwarded-For")); len(chain) > 0 {
		chain = append(chain, remote)
		for i := len(chain) - 1; i >= 0; i-- {
			if !r.isTrusted(chain[i]) {
				return chain[i].String()
			}
		}
		return chain[0].String()
	}

	if ip := net.ParseIP(strings.TrimSpace(req.Header.Get("X-Real-IP"))); ip != nil {
		return ip.String()
	}
	return remote.String()
}

func (r *Resolver) isTrusted(ip net.IP) bool {
	for _, network := range r.trusted {
		if network.Contains(ip) {
			return true
		}
	}
	return false
}

func forwardedIPs(header string) []net.IP {
	parts := strings.Split(header, ",")
	chain := make([]net.IP, 0, len(parts))
	for _, part := range parts {
		ip := net.ParseIP(strings.TrimSpace(part))
		if ip != nil {
			chain = append(chain, ip)
		}
	}
	return chain
}

func parseIP(addr string) net.IP {
	if ip := net.ParseIP(strings.TrimSpace(addr)); ip != nil {
		return ip
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return nil
	}
	return net.ParseIP(strings.TrimSpace(host))
}

func ipNet(ip net.IP) *net.IPNet {
	bits := 128
	if ip.To4() != nil {
		ip = ip.To4()
		bits = 32
	}
	return &net.IPNet{
		IP:   ip,
		Mask: net.CIDRMask(bits, bits),
	}
}
