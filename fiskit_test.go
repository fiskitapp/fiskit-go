package fiskit

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const authorized = `{"id":"inv_1","object":"invoice","status":"authorized","voucher_type":"invoice_c",
"point_of_sale":1,"number":42,"cae":"76451234567890","cae_due_date":"2026-11-20",
"authority_observations":[],"authority_errors":[]}`

func newTestClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client, err := New("sk_test_x", WithBaseURL(server.URL))
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func TestCreateSendsTheKeyTheBodyAndTheIdempotencyKey(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/invoices" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer sk_test_x" {
			t.Errorf("authorization = %q", got)
		}
		if got := r.Header.Get("Idempotency-Key"); got != "order-8812" {
			t.Errorf("idempotency key = %q", got)
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["voucher_type"] != "invoice_c" {
			t.Errorf("body = %v", body)
		}
		w.Write([]byte(authorized))
	})

	invoice, err := client.Invoices.Create(context.Background(), map[string]any{
		"voucher_type": "invoice_c",
		"amounts":      map[string]any{"total": 1500},
	}, "order-8812")
	if err != nil {
		t.Fatal(err)
	}
	if !invoice.Authorized() || *invoice.Number != 42 || invoice.CAE != "76451234567890" {
		t.Errorf("invoice = %+v", invoice)
	}
	if invoice.Raw["cae_due_date"] != "2026-11-20" {
		t.Errorf("raw = %v", invoice.Raw)
	}
}

func TestCreateGeneratesAnIdempotencyKeyAndRetriesWithTheSameOne(t *testing.T) {
	var calls atomic.Int32
	var keys []string
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		keys = append(keys, r.Header.Get("Idempotency-Key"))
		if calls.Add(1) == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Write([]byte(authorized))
	})

	if _, err := client.Invoices.Create(context.Background(), map[string]any{}, ""); err != nil {
		t.Fatal(err)
	}
	if len(keys) != 2 || keys[0] == "" || keys[0] != keys[1] {
		t.Errorf("keys = %v", keys)
	}
}

func TestAPIErrorsCarryTheirCode(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":{"type":"invalid_request_error","code":"parameter_invalid",
"message":"The total is wrong","param":"amounts.total","request_id":"req_1"}}`))
	})

	_, err := client.Invoices.Create(context.Background(), map[string]any{}, "k")
	var apiErr *Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v", err)
	}
	if apiErr.Status != 400 || apiErr.Code != "parameter_invalid" || apiErr.Param != "amounts.total" || apiErr.RequestID != "req_1" {
		t.Errorf("error = %+v", apiErr)
	}
}

func TestPostWithoutIdempotencyKeyIsNotRetried(t *testing.T) {
	var calls atomic.Int32
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	})

	if _, err := client.Invoices.Email(context.Background(), "inv_1", ""); err == nil {
		t.Fatal("expected an error")
	}
	if calls.Load() != 1 {
		t.Errorf("calls = %d", calls.Load())
	}
}

func TestPathSegmentsAreEscaped(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.EscapedPath() != "/v1/invoices/a%2F..%2Fb" {
			t.Errorf("path = %s", r.URL.EscapedPath())
		}
		w.Write([]byte(authorized))
	})

	if _, err := client.Invoices.Retrieve(context.Background(), "a/../b"); err != nil {
		t.Fatal(err)
	}
}

func TestPDFAndEmailAndTaxpayer(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/invoices/inv_1/pdf":
			if r.Header.Get("Accept") != "application/pdf" {
				t.Errorf("accept = %s", r.Header.Get("Accept"))
			}
			w.Write([]byte("%PDF-1.7"))
		case "/v1/invoices/inv_1/email":
			body, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(body), "customer@example.com") {
				t.Errorf("body = %s", body)
			}
			w.Write([]byte(`{"email":"customer@example.com"}`))
		case "/v1/taxpayers/30712345671":
			w.Write([]byte(`{"object":"taxpayer","cuit":30712345671,"name":"ACME SA","type":"company","active":true,
"vat_condition":"registered","receiver_vat_condition_id":1,"monotributo_category":null,
"address":{"street":"AV CORRIENTES 1234","city":"CABA","province":"CABA","postal_code":"1043"}}`))
		default:
			t.Errorf("unexpected %s", r.URL.Path)
		}
	})
	ctx := context.Background()

	pdf, err := client.Invoices.PDF(ctx, "inv_1")
	if err != nil || string(pdf) != "%PDF-1.7" {
		t.Errorf("pdf = %q, %v", pdf, err)
	}
	sent, err := client.Invoices.Email(ctx, "inv_1", "customer@example.com")
	if err != nil || sent != "customer@example.com" {
		t.Errorf("email = %q, %v", sent, err)
	}
	taxpayer, err := client.Taxpayers.Retrieve(ctx, 30712345671)
	if err != nil || taxpayer.Name != "ACME SA" || taxpayer.ReceiverVATConditionID != 1 || taxpayer.Address.PostalCode != "1043" {
		t.Errorf("taxpayer = %+v, %v", taxpayer, err)
	}
}

func TestNewNeedsAKey(t *testing.T) {
	t.Setenv("FISKIT_API_KEY", "")
	if _, err := New(""); err == nil {
		t.Fatal("expected an error")
	}
	t.Setenv("FISKIT_API_KEY", "sk_test_env")
	if client, err := New(""); err != nil || client.apiKey != "sk_test_env" {
		t.Fatalf("client = %v, %v", client, err)
	}
}

// Test vector from the Standard Webhooks specification.
func TestVerifyWebhook(t *testing.T) {
	secret := "whsec_MfKQ9r8GKYqrTwjUPD8ILPZIo2LaLaSw"
	body := []byte(`{"test": 2432232314}`)
	header := http.Header{}
	header.Set("webhook-id", "msg_p5jXN8AQM9LWM0D4loKWxJek")
	header.Set("webhook-timestamp", "1614265330")
	header.Set("webhook-signature", "v1,g0hM9SsE+OTPJTGt/tmIKtSyZlE3uFJELVlNIOLJ1OE=")
	at := time.Unix(1614265330, 0)

	if _, err := VerifyWebhookAt(body, header, secret, at, DefaultWebhookTolerance); err != nil {
		t.Fatalf("valid delivery rejected: %v", err)
	}
	if _, err := VerifyWebhookAt([]byte(`{"test": 1}`), header, secret, at, DefaultWebhookTolerance); !errors.Is(err, ErrInvalidWebhook) {
		t.Errorf("tampered body accepted: %v", err)
	}
	if _, err := VerifyWebhookAt(body, header, secret, at.Add(10*time.Minute), DefaultWebhookTolerance); !errors.Is(err, ErrInvalidWebhook) {
		t.Errorf("old delivery accepted: %v", err)
	}
}

func TestVerifyWebhookReturnsTheInvoice(t *testing.T) {
	secret := "whsec_MfKQ9r8GKYqrTwjUPD8ILPZIo2LaLaSw"
	body := []byte(`{"id":"evt_1","object":"event","type":"invoice.authorized","created":"2026-11-10T14:03:22Z",
"livemode":true,"data":{"object":` + authorized + `}}`)
	now := time.Now()
	header := http.Header{}
	header.Set("webhook-id", "evt_1")
	header.Set("webhook-timestamp", strconvItoa(now.Unix()))
	header.Set("webhook-signature", "v1,"+sign(secret, "evt_1", now.Unix(), body))

	event, err := VerifyWebhook(body, header, secret)
	if err != nil {
		t.Fatal(err)
	}
	if event.Type != "invoice.authorized" || event.Data.Object.CAE != "76451234567890" || event.Data.Object.Raw["id"] != "inv_1" {
		t.Errorf("event = %+v", event)
	}
}
