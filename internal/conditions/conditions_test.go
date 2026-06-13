package conditions

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"w8nc/internal/models"
)

func TestStatusCodeEqualsCanFireOnFirstPing(t *testing.T) {
	status := 200
	endpoint := models.Endpoint{NotifyCondition: condition(t, `{"type":"status_code_equals","value":200}`)}
	result := models.PingResult{StatusCode: &status, FinishedAt: time.Now()}
	evaluation, err := Evaluate(endpoint, result)
	if err != nil {
		t.Fatal(err)
	}
	if !evaluation.Matched {
		t.Fatal("expected status code condition to match")
	}
}

func TestStatusCodeChangedUsesBaseline(t *testing.T) {
	current := 302
	baseline := 200
	endpoint := models.Endpoint{NotifyCondition: condition(t, `{"type":"status_code_changed"}`), BaselineStatusCode: &baseline}
	result := models.PingResult{StatusCode: &current, FinishedAt: time.Now()}
	evaluation, err := Evaluate(endpoint, result)
	if err != nil {
		t.Fatal(err)
	}
	if !evaluation.Matched {
		t.Fatal("expected status change to match")
	}

	endpoint.BaselineStatusCode = nil
	evaluation, err = Evaluate(endpoint, result)
	if err != nil {
		t.Fatal(err)
	}
	if evaluation.Matched {
		t.Fatal("first ping should only establish baseline")
	}
}

func TestResponseLengthChangedSkipsTruncated(t *testing.T) {
	length := int64(120)
	baseline := int64(100)
	endpoint := models.Endpoint{NotifyCondition: condition(t, `{"type":"response_length_changed","tolerance_bytes":5}`), BaselineResponseLength: &baseline}
	result := models.PingResult{ResponseLength: &length, Truncated: true, FinishedAt: time.Now()}
	evaluation, err := Evaluate(endpoint, result)
	if err != nil {
		t.Fatal(err)
	}
	if evaluation.Matched {
		t.Fatal("truncated length response should not match")
	}
}

func TestRenderTemplate(t *testing.T) {
	status := 200
	length := int64(12)
	endpoint := models.Endpoint{URL: "https://example.com", HTTPMethod: "GET", NotifyCondition: condition(t, `{"type":"status_code_equals","value":200}`)}
	result := models.PingResult{StatusCode: &status, ResponseLength: &length, DurationMS: 33, FinishedAt: time.Date(2026, 6, 12, 1, 2, 3, 0, time.UTC)}
	rendered := RenderTemplate("{{method}} {{url}} {{status_code}} {{response_length}}", endpoint, result, Evaluation{ConditionType: "status_code_equals"}, "UTC")
	if !strings.Contains(rendered, "GET https://example.com 200 12") {
		t.Fatalf("unexpected rendered template: %q", rendered)
	}
}

func TestRenderTemplateCheckedAtUsesTimezone(t *testing.T) {
	endpoint := models.Endpoint{URL: "https://example.com", HTTPMethod: "GET"}
	result := models.PingResult{FinishedAt: time.Date(2026, 6, 12, 1, 2, 3, 0, time.UTC)}
	rendered := RenderTemplate("{{checked_at}}", endpoint, result, Evaluation{}, "Asia/Yekaterinburg")
	if rendered != "2026-06-12T06:02:03+05:00" {
		t.Fatalf("checked_at did not use timezone: %q", rendered)
	}
}

func TestRenderTemplateResponseBodyAndHeaders(t *testing.T) {
	endpoint := models.Endpoint{URL: "https://example.com", HTTPMethod: "GET"}
	result := models.PingResult{
		FinishedAt: time.Now(),
		Body:       []byte("hello response"),
		ResponseHeaders: map[string][]string{
			"X-Trace":      {"abc123"},
			"Content-Type": {"text/plain"},
		},
	}
	rendered := RenderTemplate("{{response_body}}\n{{response_headers}}", endpoint, result, Evaluation{}, "UTC")
	if !strings.Contains(rendered, "hello response") {
		t.Fatalf("response body missing from template: %q", rendered)
	}
	if !strings.Contains(rendered, "Content-Type: text/plain\nX-Trace: abc123") {
		t.Fatalf("response headers missing or unsorted in template: %q", rendered)
	}
}

func TestTemplatePlaceholdersIncludesRuntimeValues(t *testing.T) {
	values := strings.Join(TemplatePlaceholders(), ",")
	for _, expected := range []string{"duration_ms", "checked_at", "condition_type", "response_body", "response_headers"} {
		if !strings.Contains(values, expected) {
			t.Fatalf("placeholder %q missing from %q", expected, values)
		}
	}
}

func TestRenderTemplateMissingVariablesAreEmpty(t *testing.T) {
	endpoint := models.Endpoint{URL: "https://example.com", HTTPMethod: "GET"}
	result := models.PingResult{FinishedAt: time.Now()}
	rendered := RenderTemplate("{{url}} {{missing_variable}}", endpoint, result, Evaluation{}, "UTC")
	if rendered != "https://example.com " {
		t.Fatalf("missing variable was not rendered empty: %q", rendered)
	}
}

func condition(t *testing.T, raw string) models.Condition {
	t.Helper()
	var condition models.Condition
	if err := json.Unmarshal([]byte(raw), &condition); err != nil {
		t.Fatal(err)
	}
	return condition
}
