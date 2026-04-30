package collect

import (
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

func isFuseDevice(name string) bool {
	return normalizeDeviceName(name) == "fuse"
}

func buildRaidDevices(raid raidSnapshot) map[string][]string {
	out := make(map[string][]string)
	for _, a := range raid.Arrays {
		name := normalizeDeviceName(a.Name)
		if name == "" {
			continue
		}
		devs := make([]string, 0, len(a.MemberStates))
		for _, m := range a.MemberStates {
			d := normalizeDeviceName(m.Name)
			if d != "" {
				devs = append(devs, d)
			}
		}
		out[name] = uniqueStrings(devs)
	}
	return out
}

func normalizeDeviceName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	if runtime.GOOS == "windows" {
		if strings.HasPrefix(name, `\\?\`) {
			name = strings.TrimPrefix(name, `\\?\`)
		}
		name = strings.TrimRight(name, `\/`)
		if vol := filepath.VolumeName(name); vol != "" {
			return vol
		}
		return filepath.Base(name)
	}
	if strings.HasPrefix(name, "/dev/") {
		if resolved, err := filepath.EvalSymlinks(name); err == nil {
			name = resolved
		}
		name = filepath.Base(name)
	} else {
		name = filepath.Base(name)
	}
	return name
}

func firstMount(mps []string) string {
	if len(mps) == 0 {
		return ""
	}
	sorted := append([]string(nil), mps...)
	sort.Strings(sorted)
	return sorted[0]
}

func uniqueStrings(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}

func buildDevicePath(name string) string {
	if name == "" {
		return ""
	}
	if runtime.GOOS == "windows" {
		return ""
	}
	if strings.Contains(name, "/") {
		return ""
	}
	return "/dev/" + name
}

type mapperAliases map[string]string

func buildMapperAliases() mapperAliases {
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		return nil
	}
	ents, err := os.ReadDir("/dev/mapper")
	if err != nil {
		return nil
	}
	out := make(map[string]string)
	for _, e := range ents {
		if e.IsDir() {
			continue
		}
		p := filepath.Join("/dev/mapper", e.Name())
		resolved, err := filepath.EvalSymlinks(p)
		if err != nil {
			continue
		}
		if _, ok := out[resolved]; ok {
			continue
		}
		out[resolved] = p
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func mapperAlias(aliases mapperAliases, kname, rawName string) string {
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		return ""
	}
	if strings.HasPrefix(rawName, "/dev/mapper/") {
		return rawName
	}
	kname = strings.TrimSpace(kname)
	if kname == "" {
		return ""
	}
	target := "/dev/" + kname
	if aliases != nil {
		if alias, ok := aliases[target]; ok {
			return alias
		}
		return ""
	}
	return ""
}

func buildRef(kind, name string) string {
	name = strings.TrimSpace(name)
	if kind == "" || name == "" {
		return ""
	}
	return kind + ":" + name
}
