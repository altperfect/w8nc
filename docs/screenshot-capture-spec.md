# Screenshot Capture on Matched Conditions

## Decision

This feature is technically possible, but it should be implemented as an asynchronous, best-effort workflow that is isolated from pinging and from the primary Telegram notification path.

The safest v1 supports browser screenshots for `GET` endpoints only. Chromium navigation cannot generally issue arbitrary methods and request bodies as the main page load, so the backend and frontend should prevent enabling screenshots for unsupported methods.

Recommended v1: implement navigation screenshots for `GET` endpoints, reject `screenshot_on_match` for non-GET methods at the API boundary, and keep the schema/service boundaries ready for a later response-rendering mode if broader request support becomes necessary.

## Goals

- Add an endpoint-level checkbox: "Attempt to take a screenshot of the page when the condition is met".
- Only attempt screenshots when the endpoint condition matched.
- Never block or degrade pinging if screenshot capture fails.
- Never block the primary condition notification if screenshot capture fails.
- If screenshot capture succeeds, send a follow-up Telegram message containing:
  - `Screenshot of <full endpoint URL>`
  - the screenshot image
- Let users view screenshot attempts later from dashboard check history.
- Show success or failure per screenshot attempt.
- For successful attempts, provide a `View` button that opens a screenshot modal.
- For failed attempts, provide a `Retry` button.
- Do not allow retry while an attempt is pending or capturing.
- Close the screenshot modal with the top-right close button or by clicking outside it.

## Non-Goals for v1

- Full browser parity for arbitrary methods with bodies, such as `POST`, `PUT`, and `PATCH`.
- Capturing authenticated browser sessions or maintaining cookies between checks.
- Capturing videos, full HAR files, or interactive browser traces.
- Retrying screenshot attempts indefinitely.
- Making screenshots part of the ping result or the condition decision.

## Current Code Attachment Points

- `internal/scheduler.Service.processEndpoint` performs the ping, evaluates the condition, renders the notification message, and calls `Store.RecordPingResult`.
- `Store.RecordPingResult` creates `endpoint_checks`, creates `notification_events` when a condition matched, and deactivates notify-once endpoints.
- `Service.DispatchNotificationsOnce` sends pending text notifications asynchronously.
- `internal/notifier.Notifier` currently sends text through ProjectDiscovery `notify`.
- `web/src/components/EndpointForm.vue` owns endpoint create/edit fields.
- `web/src/pages/DashboardPage.vue` owns the check-history modal.

These boundaries are suitable for screenshot capture if the screenshot work is queued from `RecordPingResult` and processed by a separate worker after the notification event is sent.

## Data Model

Add endpoint option:

```sql
ALTER TABLE endpoints
ADD COLUMN screenshot_on_match BOOLEAN NOT NULL DEFAULT FALSE;
```

Add screenshot attempts:

```sql
CREATE TABLE screenshot_attempts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    endpoint_id UUID NOT NULL REFERENCES endpoints(id) ON DELETE CASCADE,
    endpoint_check_id UUID NOT NULL REFERENCES endpoint_checks(id) ON DELETE CASCADE,
    notification_event_id UUID NULL REFERENCES notification_events(id) ON DELETE SET NULL,
    status TEXT NOT NULL DEFAULT 'pending',
    error TEXT NULL,
    image_path TEXT NULL,
    image_content_type TEXT NULL,
    image_size_bytes BIGINT NULL,
    capture_started_at TIMESTAMPTZ NULL,
    capture_finished_at TIMESTAMPTZ NULL,
    telegram_sent_at TIMESTAMPTZ NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_screenshot_attempts_pending
ON screenshot_attempts(status, created_at);

CREATE INDEX idx_screenshot_attempts_check
ON screenshot_attempts(endpoint_check_id, created_at DESC);
```

Status values:

- `pending`: queued and waiting for the primary notification to be sent.
- `capturing`: worker has started browser capture.
- `succeeded`: image stored and Telegram follow-up sent or ready to view.
- `failed`: capture or Telegram screenshot delivery failed.
- `unsupported`: endpoint request cannot be represented by the v1 browser capture mode.

Image storage:

- Store PNG files under `/app/data/screenshots/<attempt-id>.png`.
- Store only metadata and path in Postgres.
- Serve image bytes through an authenticated API route, not directly as static files.
- Delete files when endpoint/check cleanup is added in the future. For v1, endpoint deletion should delete DB rows through cascade, but file cleanup needs an explicit best-effort store method.

## Request Fidelity

The screenshot request should reuse as much of the GET endpoint configuration as Chromium supports:

- endpoint URL
- HTTP method, constrained to `GET`
- configured headers, after decrypting masked headers
- SOCKS5 proxy, if supported by the browser driver
- request timeout
- private-target policy as closely as possible

No raw HTTP request text is stored or sent in Telegram for v1.

## Browser Driver Recommendation

Use a real Chromium-based capture layer behind an interface:

```go
type Capturer interface {
    Capture(ctx context.Context, endpoint models.Endpoint) (ScreenshotResult, error)
}
```

The implementation should be replaceable because proxy auth and arbitrary request support vary across Go browser drivers.

Recommended implementation direction:

- Prefer Playwright-backed Chromium if SOCKS5 username/password must work reliably.
- Use `chromedp` only if v1 does not need authenticated SOCKS5 proxy screenshots, because Chrome proxy auth is awkward through raw command-line flags.
- Add runtime config:
  - `SCREENSHOTS_ENABLED`
  - `SCREENSHOT_CHROME_PATH`
  - `SCREENSHOT_TIMEOUT`
  - `SCREENSHOT_VIEWPORT_WIDTH`
  - `SCREENSHOT_VIEWPORT_HEIGHT`
  - `SCREENSHOT_MAX_CONCURRENCY`
  - `SCREENSHOT_STORAGE_PATH`

Docker impact:

- Runtime image needs Chromium and fonts.
- Keep the app rootless; Chromium should run under the existing `w8nc` user.
- In Docker, Chromium may require container-safe flags such as `--headless=new`, `--disable-dev-shm-usage`, and possibly `--no-sandbox`. If `--no-sandbox` is needed, document it as a container tradeoff.

## Workflow

1. Scheduled or manual ping runs normally.
2. Condition is evaluated normally.
3. `RecordPingResult` records the endpoint check and text notification event exactly as it does today.
4. If `endpoint.screenshot_on_match` is true and the condition matched, `RecordPingResult` also creates a `screenshot_attempts` row tied to the check and notification event.
5. The normal notification worker sends the primary text notification.
6. A separate screenshot worker selects pending attempts whose notification event is sent.
7. The screenshot worker marks the attempt as `capturing`.
8. It loads the endpoint, launches Chromium, captures a PNG, and stores it under `/app/data/screenshots`.
9. If capture succeeds, it sends a Telegram follow-up with the screenshot.
10. It marks the attempt as `succeeded` or `failed`/`unsupported`.

This ordering keeps the user-visible notification independent from screenshot success.

## Telegram Delivery

Keep ProjectDiscovery `notify` for the existing text notification.

For screenshot images, add a direct Telegram Bot API sender using the existing encrypted bot token, chat ID, and Telegram SOCKS5 proxy settings.

Reason: Telegram media upload is a different capability from the current `notify` text pipeline, and the app already owns the encrypted Telegram credentials.

Recommended behavior:

- Send the screenshot as a Telegram photo with caption `Screenshot of <url>`.
- Truncate only if the URL makes the caption exceed Telegram's caption limit.

## API Changes

Endpoint create/update/list:

- Add `screenshot_on_match` to `Endpoint` and `EndpointInput`.

Check history:

- Either extend each `EndpointCheck` with `screenshot_attempts`, or add `GET /api/endpoints/{id}/checks` to join the latest attempt per check.
- Include:
  - attempt id
  - status
  - error
  - created/captured timestamps
  - `image_available`

Image view:

- `GET /api/screenshot-attempts/{id}/image`
- Auth required.
- Verify the attempt belongs to an endpoint visible to the user.
- Return `404` if missing or not successful.
- Return `Content-Type: image/png`.

Retry:

- `POST /api/screenshot-attempts/{id}/retry`
- Auth required.
- Allowed only when the current attempt status is `failed`.
- Returns `409 Conflict` for `pending`, `capturing`, `succeeded`, or `unsupported`.
- Resets the attempt to `pending` so the worker can capture it again.

## Frontend Changes

Create/edit endpoint modal:

- Add checkbox near condition/notification settings:
  - Label: "Screenshot on match"
  - Description: "Attempt to take a screenshot of the page when the condition is met."
- Disable the checkbox when the method is not `GET`.
- Add a tooltip near the checkbox:
  - "Screenshotting is supported only with GET methods. Instead, you can use the response_body placeholder in the notification template."

Check history modal:

- Add a screenshot column or a compact row detail under each matching check.
- Show:
  - `Not requested`
  - `Pending`
  - `Capturing`
  - `Succeeded` with `View`
  - `Failed` with short error and `Retry`
  - `Unsupported`

Screenshot view modal:

- Opens from the check-history `View` button.
- Uses the same modal style as existing dashboard modals.
- Shows the PNG in a constrained, scrollable area.
- Closes via top-right `X` and click outside.

## Failure Handling

Screenshot failures must not:

- change endpoint state
- change check result
- block endpoint deactivation after notify-once match
- block the primary notification event
- retry pinging
- prevent future scheduler ticks

Failures should:

- mark the attempt `failed` or `unsupported`
- store a short error string
- be visible in check history
- be logged with endpoint id, check id, and attempt id
- be retryable from check history when status is `failed`

## Security Considerations

- Reuse the same private-target restrictions where possible.
- Do not expose screenshots through unauthenticated static file paths.
- Use a fresh, temporary browser profile per attempt.
- Disable downloads.
- Apply strict timeout and max concurrency.
- Do not persist cookies.
- Be careful with decrypted sensitive headers: they are injected into the browser request for capture.
- Consider redaction controls before implementing if users may store secrets in headers.
- Browser subresource loading can reach hosts that the original ping did not directly request; this is the main SSRF/security gap to address before enabling screenshots by default.

## Test Plan

Backend:

- endpoint create/update persists `screenshot_on_match`
- endpoint create/update rejects `screenshot_on_match` for non-GET methods
- condition match creates a screenshot attempt only when enabled
- failed screenshot attempt does not prevent notification event creation
- screenshot worker ignores attempts until the primary notification event is sent
- worker marks attempts unsupported if stored data bypasses GET-only validation
- retry resets a failed attempt to pending and rejects unfinished attempts
- image API requires authentication
- image API returns PNG for successful attempts

Frontend:

- create/edit modal sends `screenshot_on_match`
- create/edit modal disables screenshot option for non-GET methods
- check history displays screenshot attempt statuses
- successful attempt has a `View` button
- failed attempt has a `Retry` button
- screenshot modal closes by `X` and backdrop click

Integration:

- local HTTP test server returns a simple HTML page
- endpoint condition matches 200 OK
- text notification event is created
- screenshot attempt succeeds
- PNG file is stored
- Telegram image sender is called with expected URL caption and image

## Open Questions

- Should screenshots be retained forever, capped by count/age, or deleted with check history?
- Should screenshot attempts run for manual `Ping now` matches, scheduled matches only, or both?
- Should the screenshot be sent as a Telegram photo or document?
