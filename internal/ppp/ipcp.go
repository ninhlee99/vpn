package ppp

import (
	"net"
)

// IPCP option types, RFC 1332 §3 (base) + RFC 1877 (DNS extensions, which
// the reference options file relies on via `ipcp-accept-remote` picking up
// whatever the LNS supplies).
const (
	IPCPOptIPAddress    = 3
	IPCPOptPrimaryDNS   = 129
	IPCPOptSecondaryDNS = 131
)

// IPv4Option builds a 4-byte IP-address-shaped IPCP option (used for both
// OptIPAddress and the DNS options, which share the same 4-byte layout).
func IPv4Option(optType uint8, ip net.IP) Option {
	v4 := ip.To4()
	if v4 == nil {
		v4 = net.IPv4zero.To4()
	}
	return Option{Type: optType, Data: append([]byte{}, v4...)}
}

// RequestIPCPOptions builds this client's IPCP Configure-Request: requested IP
// address (or 0.0.0.0 meaning "assign me one", matching
// `ipcp-accept-local`/`ipcp-accept-remote` in the reference PPP options)
// plus a request for the LNS to supply DNS servers.
func RequestIPCPOptions(requestedIP net.IP) []Option {
	ip := net.IPv4zero
	if requestedIP != nil && !requestedIP.IsUnspecified() && requestedIP.To4() != nil {
		ip = requestedIP.To4()
	}
	return []Option{
		IPv4Option(IPCPOptIPAddress, ip),
		IPv4Option(IPCPOptPrimaryDNS, net.IPv4zero),
		IPv4Option(IPCPOptSecondaryDNS, net.IPv4zero),
	}
}

// ParseIPv4Option reads a 4-byte IP-address-shaped option's value.
func ParseIPv4Option(o Option) (net.IP, bool) {
	if len(o.Data) != 4 {
		return nil, false
	}
	return net.IPv4(o.Data[0], o.Data[1], o.Data[2], o.Data[3]), true
}

// NegotiatedIPCP is the result of a completed IPCP negotiation: our
// assigned address, the LNS's own inside address (needed to configure the
// utun interface as a point-to-point link), and whatever DNS servers the
// LNS handed out (may be none — the reference server may not push DNS at
// all, in which case the engine keeps the original resolver config,
// matching the goal's "never silently hand out public DNS the server
// didn't provide").
type NegotiatedIPCP struct {
	LocalIP    net.IP
	PeerIP     net.IP
	PrimaryDNS net.IP
	SecondDNS  net.IP
}

// ApplyOption applies one option from *our own* final, peer-acked
// Configure-Request — the values this side actually ended up using.
func (n *NegotiatedIPCP) ApplyOption(o Option) {
	ip, ok := ParseIPv4Option(o)
	if !ok {
		return
	}
	switch o.Type {
	case IPCPOptIPAddress:
		n.LocalIP = ip
	case IPCPOptPrimaryDNS:
		n.PrimaryDNS = ip
	case IPCPOptSecondaryDNS:
		n.SecondDNS = ip
	}
}

// ApplyPeerOption applies one option from the *peer's* (LNS's) own
// Configure-Request — only its IP-Address option is meaningful to us, as
// the LNS's inside address, needed to bring up the utun interface as a
// point-to-point link to it.
func (n *NegotiatedIPCP) ApplyPeerOption(o Option) {
	if o.Type != IPCPOptIPAddress {
		return
	}
	if ip, ok := ParseIPv4Option(o); ok {
		n.PeerIP = ip
	}
}
