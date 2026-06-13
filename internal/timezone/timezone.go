package timezone

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

var fallbackOptions = []string{
	"UTC",
	"Africa/Cairo",
	"Africa/Johannesburg",
	"America/Anchorage",
	"America/Argentina/Buenos_Aires",
	"America/Bogota",
	"America/Chicago",
	"America/Denver",
	"America/Los_Angeles",
	"America/Mexico_City",
	"America/New_York",
	"America/Phoenix",
	"America/Sao_Paulo",
	"America/Toronto",
	"America/Vancouver",
	"Asia/Almaty",
	"Asia/Bangkok",
	"Asia/Dubai",
	"Asia/Hong_Kong",
	"Asia/Jakarta",
	"Asia/Jerusalem",
	"Asia/Kolkata",
	"Asia/Seoul",
	"Asia/Shanghai",
	"Asia/Singapore",
	"Asia/Tokyo",
	"Asia/Yekaterinburg",
	"Australia/Melbourne",
	"Australia/Perth",
	"Australia/Sydney",
	"Europe/Amsterdam",
	"Europe/Berlin",
	"Europe/Istanbul",
	"Europe/London",
	"Europe/Madrid",
	"Europe/Moscow",
	"Europe/Paris",
	"Europe/Rome",
	"Pacific/Auckland",
}

func DefaultName() string {
	for _, value := range []string{os.Getenv("TZ"), fileValue("/etc/timezone"), localtimeLinkName(), time.Local.String()} {
		name := strings.TrimSpace(value)
		if name == "" || name == "Local" || strings.HasPrefix(name, ":") {
			continue
		}
		if valid(name) {
			return name
		}
	}
	return "UTC"
}

func Normalize(name string) (string, error) {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" || trimmed == "Local" {
		trimmed = DefaultName()
	}
	if _, err := time.LoadLocation(trimmed); err != nil {
		return "", err
	}
	return trimmed, nil
}

func Location(name string) *time.Location {
	normalized, err := Normalize(name)
	if err != nil {
		normalized = DefaultName()
	}
	location, err := time.LoadLocation(normalized)
	if err != nil {
		return time.UTC
	}
	return location
}

func Options(current string) []string {
	seen := map[string]bool{}
	for _, name := range zoneTabOptions() {
		seen[name] = true
	}
	if len(seen) == 0 {
		for _, name := range fallbackOptions {
			seen[name] = true
		}
	}
	for _, name := range []string{"UTC", DefaultName(), current} {
		normalized, err := Normalize(name)
		if err == nil {
			seen[normalized] = true
		}
	}
	options := make([]string, 0, len(seen))
	for name := range seen {
		options = append(options, name)
	}
	sort.Strings(options)
	return options
}

func valid(name string) bool {
	if name == "" || strings.HasPrefix(name, "/") || strings.Contains(name, `\`) || strings.Contains(name, "..") {
		return false
	}
	_, err := time.LoadLocation(name)
	return err == nil
}

func fileValue(path string) string {
	content, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(strings.SplitN(string(content), "\n", 2)[0])
}

func localtimeLinkName() string {
	target, err := os.Readlink("/etc/localtime")
	if err != nil {
		return ""
	}
	marker := string(filepath.Separator) + "zoneinfo" + string(filepath.Separator)
	index := strings.Index(target, marker)
	if index == -1 {
		return ""
	}
	return target[index+len(marker):]
}

func zoneTabOptions() []string {
	seen := map[string]bool{}
	for _, path := range []string{"/usr/share/zoneinfo/zone1970.tab", "/usr/share/zoneinfo/zone.tab"} {
		content, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(content), "\n") {
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			parts := strings.Split(line, "\t")
			if len(parts) < 3 {
				continue
			}
			name := strings.TrimSpace(parts[2])
			if valid(name) {
				seen[name] = true
			}
		}
	}
	options := make([]string, 0, len(seen))
	for name := range seen {
		options = append(options, name)
	}
	return options
}
