package fiskit

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
)

// AuthorityMessage is an observation or error returned by ARCA.
type AuthorityMessage struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Invoice is an invoice as the API returns it. Raw holds every field of the response.
type Invoice struct {
	ID                    string             `json:"id"`
	Object                string             `json:"object"`
	Status                string             `json:"status"` // pending, processing, authorized, rejected, reconciling or failed
	VoucherType           string             `json:"voucher_type"`
	PointOfSale           int                `json:"point_of_sale"`
	Number                *int64             `json:"number"` // nil until authorized
	CAE                   string             `json:"cae"`
	CAEDueDate            string             `json:"cae_due_date"` // YYYY-MM-DD
	AuthorityObservations []AuthorityMessage `json:"authority_observations"`
	AuthorityErrors       []AuthorityMessage `json:"authority_errors"`
	Raw                   map[string]any     `json:"-"`
}

// Authorized reports whether ARCA authorized the invoice (it has a number and a CAE).
func (i *Invoice) Authorized() bool { return i.Status == "authorized" }

// Pending reports whether FisKit is still working on it: look it up later or wait for the webhook.
func (i *Invoice) Pending() bool {
	return i.Status == "pending" || i.Status == "processing" || i.Status == "reconciling"
}

// Address is a fiscal address from the ARCA registry.
type Address struct {
	Street     string `json:"street"`
	City       string `json:"city"`
	Province   string `json:"province"`
	PostalCode string `json:"postal_code"`
}

// Taxpayer is a CUIT as the ARCA registry describes it.
type Taxpayer struct {
	CUIT                   int64          `json:"cuit"`
	Name                   string         `json:"name"`
	Type                   string         `json:"type"` // person or company
	Active                 bool           `json:"active"`
	VATCondition           string         `json:"vat_condition"`
	ReceiverVATConditionID int            `json:"receiver_vat_condition_id"` // send it as receiver.vat_condition_id
	MonotributoCategory    *string        `json:"monotributo_category"`
	Address                *Address       `json:"address"`
	Raw                    map[string]any `json:"-"`
}

// InvoicesService issues and looks up invoices.
type InvoicesService struct{ client *Client }

// Create issues an invoice. body is any value that encodes to the request JSON (a map or a struct).
//
// Pass your own idempotencyKey, for example your sale id: retrying with it never issues a second
// invoice. When empty, one is generated so the automatic retries are still safe.
//
// A 202 is not an error: the returned invoice is Pending and FisKit finishes it in the background.
func (s *InvoicesService) Create(ctx context.Context, body any, idempotencyKey string) (*Invoice, error) {
	if idempotencyKey == "" {
		idempotencyKey = newIdempotencyKey()
	}
	data, err := s.client.do(ctx, request{method: http.MethodPost, path: "/v1/invoices", body: body, idempotencyKey: idempotencyKey})
	if err != nil {
		return nil, err
	}
	return decodeInvoice(data)
}

// Retrieve looks up an invoice by id, for example after a 202.
func (s *InvoicesService) Retrieve(ctx context.Context, id string) (*Invoice, error) {
	data, err := s.client.do(ctx, request{method: http.MethodGet, path: "/v1/invoices/" + url.PathEscape(id)})
	if err != nil {
		return nil, err
	}
	return decodeInvoice(data)
}

// PDF returns the voucher PDF, in ARCA's layout and with its QR code.
func (s *InvoicesService) PDF(ctx context.Context, id string) ([]byte, error) {
	return s.client.do(ctx, request{method: http.MethodGet, path: "/v1/invoices/" + url.PathEscape(id) + "/pdf", accept: "application/pdf"})
}

// Email sends the PDF to the customer and returns the address it went to. With an empty email it goes
// to the receiver.email of the invoice.
func (s *InvoicesService) Email(ctx context.Context, id, email string) (string, error) {
	body := map[string]string{}
	if email != "" {
		body["email"] = email
	}
	data, err := s.client.do(ctx, request{method: http.MethodPost, path: "/v1/invoices/" + url.PathEscape(id) + "/email", body: body})
	if err != nil {
		return "", err
	}
	var sent struct {
		Email string `json:"email"`
	}
	if err := json.Unmarshal(data, &sent); err != nil {
		return "", fmt.Errorf("fiskit: decoding the response: %w", err)
	}
	return sent.Email, nil
}

// TaxpayersService queries the ARCA registry.
type TaxpayersService struct{ client *Client }

// Retrieve returns the name, address and VAT condition of a CUIT.
func (s *TaxpayersService) Retrieve(ctx context.Context, cuit int64) (*Taxpayer, error) {
	data, err := s.client.do(ctx, request{method: http.MethodGet, path: "/v1/taxpayers/" + strconv.FormatInt(cuit, 10)})
	if err != nil {
		return nil, err
	}
	var t Taxpayer
	if err := decode(data, &t, &t.Raw); err != nil {
		return nil, err
	}
	return &t, nil
}

func decodeInvoice(data []byte) (*Invoice, error) {
	var i Invoice
	if err := decode(data, &i, &i.Raw); err != nil {
		return nil, err
	}
	return &i, nil
}

func decode(data []byte, target any, raw *map[string]any) error {
	if err := json.Unmarshal(data, target); err != nil {
		return fmt.Errorf("fiskit: decoding the response: %w", err)
	}
	if err := json.Unmarshal(data, raw); err != nil {
		return fmt.Errorf("fiskit: decoding the response: %w", err)
	}
	return nil
}
