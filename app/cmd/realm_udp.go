package cmd

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"time"

	eUtils "github.com/apernet/hysteria/extras/v2/utils"
)

type realmLocalUDPFamily uint8

const (
	realmLocalUDPFamilyAuto realmLocalUDPFamily = iota
	realmLocalUDPFamilyIPv4
	realmLocalUDPFamilyIPv6
)

type realmLocalUDPOptions struct {
	Ports           eUtils.PortUnion
	PreferFamily    realmLocalUDPFamily
	FallbackTimeout time.Duration
}

type realmLocalUDPListenError struct {
	Phase string
	Err   error
}

func (e *realmLocalUDPListenError) Error() string {
	return fmt.Sprintf("realm local UDP %s failed: %v", e.Phase, e.Err)
}

func (e *realmLocalUDPListenError) Unwrap() error {
	return e.Err
}

type realmUDPListenFunc func(*net.UDPAddr) (net.PacketConn, error)

type realmUDPDiscoverFunc func(context.Context, net.PacketConn) ([]netip.AddrPort, error)

func makeRealmLocalUDPOptions(listenPorts, preferIPVersion string, fallbackTimeout time.Duration, legacyLocalPort int) (realmLocalUDPOptions, error) {
	if fallbackTimeout < 0 {
		return realmLocalUDPOptions{}, errors.New("fallbackTimeout must not be negative")
	}
	if legacyLocalPort != 0 && listenPorts != "" {
		return realmLocalUDPOptions{}, errors.New("listenPorts cannot be used together with legacy lport")
	}
	preferFamily, err := parseRealmLocalUDPFamily(preferIPVersion)
	if err != nil {
		return realmLocalUDPOptions{}, err
	}
	ports, err := parseRealmLocalUDPPorts(listenPorts, legacyLocalPort)
	if err != nil {
		return realmLocalUDPOptions{}, err
	}
	return realmLocalUDPOptions{
		Ports:           ports,
		PreferFamily:    preferFamily,
		FallbackTimeout: fallbackTimeout,
	}, nil
}

func parseRealmLocalUDPFamily(s string) (realmLocalUDPFamily, error) {
	normalized := strings.NewReplacer("_", "", "-", "", " ", "").Replace(strings.ToLower(strings.TrimSpace(s)))
	switch normalized {
	case "", "auto":
		return realmLocalUDPFamilyAuto, nil
	case "v4", "ipv4", "preferv4", "preferipv4":
		return realmLocalUDPFamilyIPv4, nil
	case "v6", "ipv6", "preferv6", "preferipv6":
		return realmLocalUDPFamilyIPv6, nil
	default:
		return realmLocalUDPFamilyAuto, errors.New("preferIPVersion must be one of auto, v4, or v6")
	}
}

func parseRealmLocalUDPPorts(listenPorts string, legacyLocalPort int) (eUtils.PortUnion, error) {
	if legacyLocalPort != 0 {
		p := uint16(legacyLocalPort)
		return eUtils.PortUnion{{Start: p, End: p}}, nil
	}
	if listenPorts == "" {
		return nil, nil
	}
	ports := eUtils.ParsePortUnion(listenPorts)
	if ports == nil {
		return nil, errors.New("listenPorts is not a valid port number or range")
	}
	for _, r := range ports {
		if r.Start == 0 || r.End == 0 {
			return nil, errors.New("listenPorts must only contain ports in 1-65535")
		}
	}
	return ports, nil
}

func openRealmUDPConn(ctx context.Context, opts realmLocalUDPOptions, listen realmUDPListenFunc, discover realmUDPDiscoverFunc) (net.PacketConn, []netip.AddrPort, error) {
	if opts.PreferFamily == realmLocalUDPFamilyAuto {
		return tryOpenRealmUDPConn(ctx, opts.Ports, realmLocalUDPFamilyAuto, listen, discover)
	}

	fallbackCtx, cancel := realmFallbackContext(ctx, opts.FallbackTimeout)
	defer cancel()
	conn, addrs, err := tryOpenRealmUDPConn(fallbackCtx, opts.Ports, opts.PreferFamily, listen, discover)
	if err == nil {
		return conn, addrs, nil
	}
	if ctx.Err() != nil {
		return nil, nil, err
	}
	otherFamily := realmLocalUDPFamilyIPv4
	if opts.PreferFamily == realmLocalUDPFamilyIPv4 {
		otherFamily = realmLocalUDPFamilyIPv6
	}
	return tryOpenRealmUDPConn(ctx, opts.Ports, otherFamily, listen, discover)
}

func tryOpenRealmUDPConn(ctx context.Context, ports eUtils.PortUnion, family realmLocalUDPFamily, listen realmUDPListenFunc, discover realmUDPDiscoverFunc) (net.PacketConn, []netip.AddrPort, error) {
	var returnConn net.PacketConn
	var returnAddrs []netip.AddrPort
	var lastBindErr error
	var lastDiscoverErr error
	tryPort := func(port uint16) bool {
		conn, err := listen(realmLocalUDPAddr(family, port))
		if err != nil {
			lastBindErr = err
			return false
		}
		if discover == nil {
			lastBindErr = nil
			returnConn = conn
			return true
		}
		addrs, err := discover(ctx, conn)
		if err == nil {
			returnConn = conn
			returnAddrs = addrs
			lastDiscoverErr = nil
			return true
		}
		_ = conn.Close()
		lastDiscoverErr = err
		if ctx.Err() != nil {
			return true
		}
		return false
	}
	if ports == nil {
		if tryPort(0) && returnConn != nil {
			return returnConn, returnAddrs, nil
		}
	} else {
		for _, r := range ports {
			for port := r.Start; ; port++ {
				if tryPort(port) {
					if returnConn != nil {
						return returnConn, returnAddrs, nil
					}
					break
				}
				if ctx.Err() != nil || port == r.End {
					break
				}
			}
			if ctx.Err() != nil {
				break
			}
		}
	}
	if lastDiscoverErr != nil {
		return nil, nil, &realmLocalUDPListenError{Phase: "discovery", Err: lastDiscoverErr}
	}
	if lastBindErr != nil {
		return nil, nil, &realmLocalUDPListenError{Phase: "bind", Err: lastBindErr}
	}
	return nil, nil, &realmLocalUDPListenError{Phase: "bind", Err: errors.New("no UDP listen candidate available")}
}

func realmFallbackContext(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, timeout)
}

func realmLocalUDPAddr(family realmLocalUDPFamily, port uint16) *net.UDPAddr {
	addr := &net.UDPAddr{Port: int(port)}
	switch family {
	case realmLocalUDPFamilyIPv4:
		addr.IP = net.IPv4zero
	case realmLocalUDPFamilyIPv6:
		addr.IP = net.IPv6zero
	}
	return addr
}
