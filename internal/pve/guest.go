//go:build linux

package pve

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"slices"
)

func guestIPs(ctx context.Context, target guestTarget) ([]string, error) {
	before, err := readProcess(target.id)
	if err != nil {
		return nil, err
	}
	if before.start != target.start {
		return nil, errors.New("VM restarted before guest query")
	}
	m, err := openMonitor(ctx, fmt.Sprintf("/run/qemu-server/%d.qga", target.id), true)
	if err != nil {
		return nil, err
	}
	defer m.Close()
	raw, err := m.call("guest-network-get-interfaces")
	if err != nil {
		return nil, err
	}
	after, err := readProcess(target.id)
	if err != nil {
		return nil, err
	}
	if before.pid != after.pid || before.start != after.start {
		return nil, errors.New("VM restarted during guest query")
	}
	return decodeIPs(raw)
}

func decodeIPs(raw []byte) ([]string, error) {
	var interfaces []struct {
		Addresses []struct {
			IP string `json:"ip-address"`
		} `json:"ip-addresses"`
	}
	if err := json.Unmarshal(raw, &interfaces); err != nil {
		return nil, err
	}
	if interfaces == nil {
		return nil, errors.New("guest agent did not return interfaces")
	}
	seen := make(map[netip.Addr]bool)
	var ips []string
	for _, iface := range interfaces {
		for _, address := range iface.Addresses {
			ip, err := netip.ParseAddr(address.IP)
			if err != nil || ip.Zone() != "" {
				continue
			}
			ip = ip.Unmap()
			if !ip.IsGlobalUnicast() || seen[ip] {
				continue
			}
			if len(ips) == 1024 {
				return nil, errors.New("too many guest IPs")
			}
			seen[ip] = true
			ips = append(ips, ip.String())
		}
	}
	slices.Sort(ips)
	return ips, nil
}
