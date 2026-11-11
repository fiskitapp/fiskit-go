package fiskit

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Event is a webhook delivery. Data.Object is the invoice with the same fields as the API response.
type Event struct {
	ID       string    `json:"id"`
	Object   string    `json:"object"`
	Type     string    `json:"type"` // invoice.authorized, invoice.rejected or invoice.failed
	Created  time.Time `json:"created"`
	Livemode bool      `json:"livemode"`
	Data     struct {
		Object Invoice `json:"object"`
	} `json:"data"`
}

// ErrInvalidWebhook is returned (wrapped) when a delivery cannot be trusted.
var ErrInvalidWebhook = errors.New("fiskit: invalid webhook")

// DefaultWebhookTolerance is how old a delivery may be before it is rejected.
const DefaultWebhookTolerance = 5 * time.Minute

// VerifyWebhook checks the Standard Webhooks signature of a delivery and returns the event. Pass the raw
// body exactly as received, before decoding it, and the secret (whsec_...) from the dashboard.
func VerifyWebhook(rawBody []byte, header http.Header, secret string) (*Event, error) {
	return VerifyWebhookAt(rawBody, header, secret, time.Now(), DefaultWebhookTolerance)
}

// VerifyWebhookAt is VerifyWebhook with an explicit clock and tolerance.
func VerifyWebhookAt(rawBody []byte, header http.Header, secret string, now time.Time, tolerance time.Duration) (*Event, error) {
	id := header.Get("webhook-id")
	timestamp := header.Get("webhook-timestamp")
	signatures := header.Get("webhook-signature")
	if id == "" || timestamp == "" || signatures == "" {
		return nil, wrap("missing webhook headers")
	}
	seconds, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return nil, wrap("invalid webhook timestamp")
	}
	if math.Abs(now.Sub(time.Unix(seconds, 0)).Seconds()) > tolerance.Seconds() {
		return nil, wrap("webhook timestamp out of tolerance")
	}
	key, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(secret, "whsec_"))
	if err != nil {
		return nil, wrap("the secret is not a valid whsec_ value")
	}

	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(id + "." + timestamp + "."))
	mac.Write(rawBody)
	expected := mac.Sum(nil)

	for _, candidate := range strings.Fields(signatures) {
		received, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(candidate, "v1,"))
		if err == nil && hmac.Equal(received, expected) {
			var event Event
			if err := json.Unmarshal(rawBody, &event); err != nil {
				return nil, wrap("invalid webhook body")
			}
			var raw struct {
				Data struct {
					Object map[string]any `json:"object"`
				} `json:"data"`
			}
			if err := json.Unmarshal(rawBody, &raw); err != nil {
				return nil, wrap("invalid webhook body")
			}
			event.Data.Object.Raw = raw.Data.Object
			return &event, nil
		}
	}
	return nil, wrap("invalid webhook signature")
}

func wrap(reason string) error {
	return &webhookError{reason: reason}
}

type webhookError struct{ reason string }

func (e *webhookError) Error() string { return "fiskit: " + e.reason }
func (e *webhookError) Unwrap() error { return ErrInvalidWebhook }
