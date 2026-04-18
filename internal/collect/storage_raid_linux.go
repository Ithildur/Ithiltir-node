//go:build linux

package collect

import (
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"Ithiltir-node/internal/metrics"
)

func collectMDRaid() []raidArraySnapshot {
	data, err := os.ReadFile("/proc/mdstat")
	if err != nil {
		return nil
	}
	return parseMDStat(string(data))
}

func parseMDStat(s string) []raidArraySnapshot {
	lines := strings.Split(s, "\n")
	arrays := make([]raidArraySnapshot, 0)

	members := func(fields []string) []string {
		names := make([]string, 0)
		for _, f := range fields {
			idx := strings.Index(f, "[")
			if idx <= 0 {
				continue
			}
			names = append(names, f[:idx])
		}
		return names
	}

	details := func(start int, array *raidArraySnapshot) (states string, next int) {
		for i := start; i < len(lines); i++ {
			raw := lines[i]
			if raw == "" {
				return states, i
			}
			if !strings.HasPrefix(raw, " ") && !strings.HasPrefix(raw, "\t") {
				return states, i - 1
			}

			l2 := strings.TrimSpace(raw)
			toks := strings.Fields(l2)
			for _, t := range toks {
				if strings.HasPrefix(t, "[") && strings.HasSuffix(t, "]") {
					inner := t[1 : len(t)-1]
					if strings.Contains(inner, "/") {
						parts := strings.SplitN(inner, "/", 2)
						if len(parts) == 2 {
							if tot, err := strconv.Atoi(parts[0]); err == nil {
								array.Devices = tot
							}
							if act, err := strconv.Atoi(parts[1]); err == nil {
								array.Active = act
							}
						}
					} else {
						states = inner
					}
				}
			}

			if strings.Contains(l2, "resync") ||
				strings.Contains(l2, "recovery") ||
				strings.Contains(l2, "reshape") ||
				strings.Contains(l2, "check") {
				array.SyncStatus = l2
				if idx := strings.Index(l2, "%"); idx > 0 {
					start := idx - 1
					for start >= 0 {
						ch := l2[start]
						if (ch >= '0' && ch <= '9') || ch == '.' {
							start--
							continue
						}
						break
					}
					start++
					if start < idx {
						array.SyncProgress = l2[start : idx+1]
					}
				}
			}
		}
		return states, len(lines)
	}

	for i := 0; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if line == "" ||
			strings.HasPrefix(line, "Personalities") ||
			strings.HasPrefix(line, "unused devices") ||
			strings.HasPrefix(line, "read_ahead") {
			continue
		}

		if !strings.HasPrefix(line, "md") {
			continue
		}

		array, memberFields, ok := parseMDLine(line)
		if !ok {
			continue
		}

		memberNames := members(memberFields)
		states, next := details(i+1, &array)
		i = next

		if states != "" && len(memberNames) == len(states) {
			array.MemberStates = make([]metrics.RaidMember, 0, len(memberNames))
			failed := 0
			for idx, n := range memberNames {
				state := "unknown"
				ch := states[idx]
				switch ch {
				case 'U', 'u':
					state = "up"
				case '_':
					state = "down"
					failed++
				default:
					state = "unknown"
				}
				array.MemberStates = append(array.MemberStates, metrics.RaidMember{
					Name:  n,
					State: state,
				})
			}
			array.Failed = failed
			if array.Devices > 0 {
				array.Working = array.Devices - failed
			}
		}

		array.Health = "unknown"
		if array.Devices > 0 {
			if array.Failed > 0 || (array.Active > 0 && array.Active < array.Devices) {
				array.Health = "degraded"
			} else {
				array.Health = "healthy"
			}
		}
		if array.Devices > 0 || len(memberNames) > 0 {
			arrays = append(arrays, array)
		}
	}

	return arrays
}

func parseMDLine(line string) (raidArraySnapshot, []string, bool) {
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return raidArraySnapshot{}, nil, false
	}

	name := strings.TrimSuffix(fields[0], ":")
	if !strings.HasPrefix(name, "md") {
		return raidArraySnapshot{}, nil, false
	}

	i := 1
	if !strings.HasSuffix(fields[0], ":") {
		if fields[i] != ":" {
			return raidArraySnapshot{}, nil, false
		}
		i++
	}
	if i >= len(fields) {
		return raidArraySnapshot{}, nil, false
	}

	array := raidArraySnapshot{Name: name, Status: fields[i]}
	i++
	for i < len(fields) {
		if mdLevel(fields[i]) {
			array.Level = fields[i]
			i++
			break
		}
		if strings.Contains(fields[i], "[") {
			break
		}
		i++
	}
	return array, fields[i:], true
}

func mdLevel(s string) bool {
	s = strings.ToLower(strings.TrimSpace(s))
	return strings.HasPrefix(s, "raid") || s == "linear" || s == "multipath"
}

func collectZFSRaid(debug bool) []raidArraySnapshot {
	if !commandExists("zpool") {
		return nil
	}

	out, err := runCmd(3*time.Second, "zpool", "status", "-P")
	if err != nil {
		if debug {
			log.Printf("DEBUG: zpool status failed: %v", err)
		}
		return nil
	}
	return parseZFSStatus(string(out))
}

func parseZFSStatus(s string) []raidArraySnapshot {
	lines := strings.Split(s, "\n")

	var res []raidArraySnapshot
	var cur *raidArraySnapshot
	inConfig := false
	skipHeader := 0

	progress := func(v string) string {
		if idx := strings.Index(v, "%"); idx > 0 {
			start := idx - 1
			for start >= 0 {
				ch := v[start]
				if (ch >= '0' && ch <= '9') || ch == '.' {
					start--
					continue
				}
				break
			}
			start++
			if start < idx {
				return v[start : idx+1]
			}
		}
		return ""
	}

	health := func(state string) string {
		switch strings.ToUpper(state) {
		case "ONLINE":
			return "healthy"
		case "DEGRADED", "FAULTED", "UNAVAIL", "OFFLINE", "REMOVED", "SUSPENDED":
			return "degraded"
		default:
			return "unknown"
		}
	}

	flush := func() {
		if cur == nil {
			return
		}
		if cur.Devices == 0 && len(cur.MemberStates) > 0 {
			cur.Devices = len(cur.MemberStates)
		}
		if (cur.Working == 0 && cur.Active == 0) && len(cur.MemberStates) > 0 {
			w, f := 0, 0
			for _, m := range cur.MemberStates {
				if m.State == "up" {
					w++
				} else if m.State == "down" {
					f++
				}
			}
			cur.Working = w
			cur.Active = w
			cur.Failed = f
		}
		if cur.Health == "" || cur.Health == "unknown" {
			cur.Health = "healthy"
			if cur.Failed > 0 {
				cur.Health = "degraded"
			}
		}
		res = append(res, *cur)
		cur = nil
	}

	leaf := func(name string) bool {
		name = strings.TrimSpace(name)
		if name == "" {
			return false
		}
		l := strings.ToLower(name)
		if strings.HasPrefix(l, "mirror") || strings.HasPrefix(l, "raidz") ||
			l == "spares" || l == "logs" || l == "cache" || l == "special" || l == "dedup" {
			return false
		}
		if cur != nil && name == cur.Name {
			return false
		}
		if strings.HasPrefix(name, "/dev/") {
			return true
		}
		for i := 0; i < len(name); i++ {
			ch := name[i]
			if (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') {
				continue
			}
			if ch == '-' {
				return false
			}
			return false
		}
		return true
	}

	levelOf := func(vdevName string) string {
		l := strings.ToLower(vdevName)
		switch {
		case strings.HasPrefix(l, "mirror"):
			return "mirror"
		case strings.HasPrefix(l, "raidz1"):
			return "raidz1"
		case strings.HasPrefix(l, "raidz2"):
			return "raidz2"
		case strings.HasPrefix(l, "raidz3"):
			return "raidz3"
		default:
			return ""
		}
	}

	for _, raw := range lines {
		line := strings.TrimRight(raw, "\r")
		trim := strings.TrimSpace(line)

		if strings.HasPrefix(trim, "pool:") {
			flush()
			name := strings.TrimSpace(strings.TrimPrefix(trim, "pool:"))
			cur = &raidArraySnapshot{Name: name, Level: "unknown", Health: "unknown"}
			inConfig = false
			skipHeader = 0
			continue
		}
		if cur == nil {
			continue
		}

		if strings.HasPrefix(trim, "state:") {
			cur.Status = strings.TrimSpace(strings.TrimPrefix(trim, "state:"))
			cur.Health = health(cur.Status)
			continue
		}
		if strings.HasPrefix(trim, "scan:") {
			cur.SyncStatus = strings.TrimSpace(strings.TrimPrefix(trim, "scan:"))
			cur.SyncProgress = progress(cur.SyncStatus)
			continue
		}

		if trim == "config:" {
			inConfig = true
			skipHeader = 2
			continue
		}
		if strings.HasPrefix(trim, "errors:") {
			inConfig = false
			continue
		}
		if !inConfig {
			continue
		}

		if skipHeader > 0 && (trim == "" || strings.HasPrefix(trim, "NAME")) {
			skipHeader--
			continue
		}
		skipHeader = 0
		if trim == "" {
			continue
		}

		fields := strings.Fields(trim)
		if len(fields) < 2 {
			continue
		}
		name := fields[0]
		state := strings.ToUpper(fields[1])

		if cur.Level == "unknown" {
			if lv := levelOf(name); lv != "" {
				cur.Level = lv
			}
		}

		if leaf(name) {
			st := "unknown"
			switch state {
			case "ONLINE":
				st = "up"
			case "DEGRADED", "FAULTED", "UNAVAIL", "OFFLINE", "REMOVED":
				st = "down"
			default:
				st = "unknown"
			}
			cur.MemberStates = append(cur.MemberStates, metrics.RaidMember{Name: name, State: st})
		}
	}

	flush()
	return res
}

func collectRaid(debug bool) raidSnapshot {
	snap := raidSnapshot{
		Supported: true,
		Available: false,
		Arrays:    nil,
	}

	md := collectMDRaid()
	if len(md) > 0 {
		snap.Arrays = append(snap.Arrays, md...)
	}

	zfs := collectZFSRaid(debug)
	if len(zfs) > 0 {
		snap.Arrays = append(snap.Arrays, zfs...)
	}

	if len(snap.Arrays) > 0 {
		snap.Available = true
	}
	return snap
}
