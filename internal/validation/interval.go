package validation

import (
	"fmt"
	"regexp"
	"strconv"
	"time"
)

var intervalPattern = regexp.MustCompile(`^[1-9][0-9]*(s|m|h|d)$`)

func ParseInterval(value string) (time.Duration, error) {
	if !intervalPattern.MatchString(value) {
		return 0, fmt.Errorf("interval must match digits plus unit: s, m, h, or d")
	}
	unit := value[len(value)-1]
	count, err := strconv.ParseInt(value[:len(value)-1], 10, 64)
	if err != nil || count <= 0 {
		return 0, fmt.Errorf("interval value must be positive")
	}
	switch unit {
	case 's':
		return time.Duration(count) * time.Second, nil
	case 'm':
		return time.Duration(count) * time.Minute, nil
	case 'h':
		return time.Duration(count) * time.Hour, nil
	case 'd':
		return time.Duration(count) * 24 * time.Hour, nil
	default:
		return 0, fmt.Errorf("unsupported interval unit")
	}
}

func FormatInterval(seconds int) string {
	duration := time.Duration(seconds) * time.Second
	switch {
	case seconds%(24*60*60) == 0:
		return fmt.Sprintf("%dd", int(duration/(24*time.Hour)))
	case seconds%(60*60) == 0:
		return fmt.Sprintf("%dh", int(duration/time.Hour))
	case seconds%60 == 0:
		return fmt.Sprintf("%dm", int(duration/time.Minute))
	default:
		return fmt.Sprintf("%ds", seconds)
	}
}

func ValidateInterval(value string, min, max time.Duration) (int, error) {
	duration, err := ParseInterval(value)
	if err != nil {
		return 0, err
	}
	if duration < min {
		return 0, fmt.Errorf("interval must be at least %s", min)
	}
	if duration > max {
		return 0, fmt.Errorf("interval must be at most %s", max)
	}
	return int(duration / time.Second), nil
}
