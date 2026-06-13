package conditions

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"w8nc/internal/models"
	tz "w8nc/internal/timezone"
)

type Evaluation struct {
	Matched        bool
	ConditionType  string
	ConditionValue string
	BaselineStatus *int
	BaselineLength *int64
}

var templateVariablePattern = regexp.MustCompile(`{{[A-Za-z0-9_]+}}`)

const templateResponseBodyRuneLimit = 12000

var templatePlaceholders = []string{
	"endpoint_name",
	"url",
	"method",
	"state",
	"condition_type",
	"condition_value",
	"status_code",
	"previous_status_code",
	"response_length",
	"previous_response_length",
	"response_body",
	"response_headers",
	"duration_ms",
	"checked_at",
	"error",
}

func TemplatePlaceholders() []string {
	values := append([]string(nil), templatePlaceholders...)
	sort.Strings(values)
	return values
}

func Evaluate(endpoint models.Endpoint, result models.PingResult) (Evaluation, error) {
	evaluation := Evaluation{
		ConditionType:  endpoint.NotifyCondition.Type,
		BaselineStatus: endpoint.BaselineStatusCode,
		BaselineLength: endpoint.BaselineResponseLength,
	}
	if result.StatusCode != nil {
		value := *result.StatusCode
		evaluation.BaselineStatus = &value
	}
	if result.ResponseLength != nil && !result.Truncated {
		value := *result.ResponseLength
		evaluation.BaselineLength = &value
	}

	switch endpoint.NotifyCondition.Type {
	case "body_contains":
		target, err := stringValue(endpoint.NotifyCondition.Value)
		if err != nil {
			return evaluation, err
		}
		evaluation.ConditionValue = target
		evaluation.Matched = target != "" && bytes.Contains(result.Body, []byte(target))
	case "status_code_equals":
		target, err := intValue(endpoint.NotifyCondition.Value)
		if err != nil {
			return evaluation, err
		}
		evaluation.ConditionValue = strconv.Itoa(target)
		evaluation.Matched = result.StatusCode != nil && *result.StatusCode == target
	case "status_code_changed":
		if endpoint.BaselineStatusCode != nil && result.StatusCode != nil {
			evaluation.Matched = *endpoint.BaselineStatusCode != *result.StatusCode
			evaluation.ConditionValue = strconv.Itoa(*endpoint.BaselineStatusCode)
		}
	case "response_length_changed":
		tolerance := int64(0)
		if endpoint.NotifyCondition.ToleranceBytes != nil {
			tolerance = *endpoint.NotifyCondition.ToleranceBytes
		}
		evaluation.ConditionValue = strconv.FormatInt(tolerance, 10)
		if endpoint.BaselineResponseLength != nil && result.ResponseLength != nil && !result.Truncated {
			diff := *endpoint.BaselineResponseLength - *result.ResponseLength
			if diff < 0 {
				diff = -diff
			}
			evaluation.Matched = diff > tolerance
		}
	default:
		return evaluation, fmt.Errorf("unsupported condition type %q", endpoint.NotifyCondition.Type)
	}
	return evaluation, nil
}

func ValidateCondition(condition models.Condition) error {
	switch condition.Type {
	case "body_contains":
		value, err := stringValue(condition.Value)
		if err != nil || value == "" {
			return fmt.Errorf("body_contains requires a non-empty value")
		}
	case "status_code_equals":
		value, err := intValue(condition.Value)
		if err != nil || value < 100 || value > 599 {
			return fmt.Errorf("status_code_equals requires a status code from 100 to 599")
		}
	case "status_code_changed":
		return nil
	case "response_length_changed":
		if condition.ToleranceBytes != nil && *condition.ToleranceBytes < 0 {
			return fmt.Errorf("tolerance_bytes must be zero or greater")
		}
	default:
		return fmt.Errorf("unsupported condition type")
	}
	return nil
}

func RenderTemplate(template string, endpoint models.Endpoint, result models.PingResult, evaluation Evaluation, timezoneName string) string {
	location := tz.Location(timezoneName)
	values := map[string]string{
		"endpoint_name":            derefString(endpoint.Name),
		"url":                      endpoint.URL,
		"method":                   endpoint.HTTPMethod,
		"state":                    stateForTemplate(endpoint.Active, result),
		"condition_type":           evaluation.ConditionType,
		"condition_value":          evaluation.ConditionValue,
		"status_code":              intValueString(result.StatusCode),
		"previous_status_code":     intValueString(endpoint.BaselineStatusCode),
		"response_length":          int64ValueString(result.ResponseLength),
		"previous_response_length": int64ValueString(endpoint.BaselineResponseLength),
		"response_body":            responseBodyString(result.Body),
		"response_headers":         responseHeadersString(result.ResponseHeaders),
		"duration_ms":              strconv.Itoa(result.DurationMS),
		"checked_at":               result.FinishedAt.In(location).Format("2006-01-02T15:04:05Z07:00"),
		"error":                    derefString(result.Error),
	}
	rendered := template
	if strings.TrimSpace(rendered) == "" {
		rendered = models.DefaultNotificationTemplate
	}
	return templateVariablePattern.ReplaceAllStringFunc(rendered, func(token string) string {
		key := strings.TrimSuffix(strings.TrimPrefix(token, "{{"), "}}")
		return values[key]
	})
}

func stateForTemplate(active bool, result models.PingResult) string {
	if !active {
		return "deactivated"
	}
	if result.StatusCode == nil {
		return "offline"
	}
	if *result.StatusCode >= 100 && *result.StatusCode <= 399 {
		return "live"
	}
	return "warning"
}

func stringValue(raw json.RawMessage) (string, error) {
	var value string
	if len(raw) == 0 {
		return "", fmt.Errorf("missing value")
	}
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", fmt.Errorf("value must be a string")
	}
	return value, nil
}

func intValue(raw json.RawMessage) (int, error) {
	var value int
	if len(raw) == 0 {
		return 0, fmt.Errorf("missing value")
	}
	if err := json.Unmarshal(raw, &value); err != nil {
		return 0, fmt.Errorf("value must be an integer")
	}
	return value, nil
}

func derefString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func intValueString(value *int) string {
	if value == nil {
		return ""
	}
	return strconv.Itoa(*value)
}

func int64ValueString(value *int64) string {
	if value == nil {
		return ""
	}
	return strconv.FormatInt(*value, 10)
}

func responseBodyString(body []byte) string {
	value := strings.ToValidUTF8(string(body), "\uFFFD")
	runes := []rune(value)
	if len(runes) <= templateResponseBodyRuneLimit {
		return value
	}
	return string(runes[:templateResponseBodyRuneLimit]) + "\n[truncated]"
}

func responseHeadersString(headers map[string][]string) string {
	if len(headers) == 0 {
		return ""
	}
	names := make([]string, 0, len(headers))
	for name := range headers {
		names = append(names, name)
	}
	sort.Strings(names)
	lines := make([]string, 0, len(names))
	for _, name := range names {
		lines = append(lines, fmt.Sprintf("%s: %s", name, strings.Join(headers[name], ", ")))
	}
	return strings.Join(lines, "\n")
}
