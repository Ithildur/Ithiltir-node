//go:build linux

package pve

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"slices"
)

func guestIPs(ctx context.Context, host string, id int, lock *os.File) ([]string, error) {
	raw, err := queryLocked(ctx, lock, fmt.Sprintf("/nodes/%s/qemu/%d/agent/network-get-interfaces", host, id))
	if err != nil {
		return nil, err
	}
	return decodeIPs(raw)
}

func decodeIPs(raw []byte) ([]string, error) {
	var reply struct {
		Result []struct {
			Addresses []struct {
				IP string `json:"ip-address"`
			} `json:"ip-addresses"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &reply); err != nil {
		return nil, err
	}
	if reply.Result == nil {
		return nil, errors.New("guest agent did not return interfaces")
	}
	seen := make(map[netip.Addr]bool)
	var ips []string
	for _, iface := range reply.Result {
		for _, address := range iface.Addresses {
			ip, err := netip.ParseAddr(address.IP)
			if err != nil || ip.Zone() != "" {
				continue
			}
			ip = ip.Unmap()
			if !ip.IsGlobalUnicast() || seen[ip] {
				continue
			}
			if len(ips) == 128 {
				return nil, errors.New("too many guest IPs")
			}
			seen[ip] = true
			ips = append(ips, ip.String())
		}
	}
	slices.Sort(ips)
	return ips, nil
}
