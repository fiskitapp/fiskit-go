# FisKit for Go

[Español más abajo](#español)

Official client of the [FisKit](https://fiskit.com.ar) API: Argentine electronic invoices with ARCA (formerly AFIP) in a single request. Go 1.21 or later, standard library only.

```bash
go get github.com/fiskitapp/fiskit-go
```

```go
import "github.com/fiskitapp/fiskit-go"

client, err := fiskit.New("") // reads FISKIT_API_KEY

// Your sale id as the key: a retry never issues a second invoice
invoice, err := client.Invoices.Create(ctx, map[string]any{
	"voucher_type": "invoice_c",
	"amounts":      map[string]any{"total": 1500},
}, "sale-8812")
if err != nil {
	var apiErr *fiskit.Error
	if errors.As(err, &apiErr) {
		log.Printf("%s: %s", apiErr.Code, apiErr.Message) // branch on Code, it is stable
	}
	return err
}
if invoice.Pending() {
	// ARCA was slow: FisKit finishes it in the background. Store invoice.ID and wait for the webhook.
}
fmt.Println(*invoice.Number, invoice.CAE)

pdf, err := client.Invoices.PDF(ctx, invoice.ID)                    // PDF with ARCA's QR code
_, err = client.Invoices.Email(ctx, invoice.ID, "customer@example.com")
taxpayer, err := client.Taxpayers.Retrieve(ctx, 30712345671)       // name and VAT condition of a CUIT
```

**Webhooks:**

```go
http.HandleFunc("/webhooks/fiskit", func(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	event, err := fiskit.VerifyWebhook(body, r.Header, os.Getenv("FISKIT_WEBHOOK_SECRET"))
	if err != nil {
		http.Error(w, "invalid signature", http.StatusBadRequest)
		return
	}
	// event.Type == "invoice.authorized"; event.Data.Object is the invoice
	w.WriteHeader(http.StatusNoContent)
})
```

Options: `fiskit.WithHTTPClient`, `fiskit.WithMaxRetries` and `fiskit.WithBaseURL`. Every call takes a `context.Context`.

`sk_test_...` keys issue against ARCA's homologation environment (no fiscal value); `sk_live_...` keys issue real invoices. Outages are retried with the same idempotency key.

Full documentation: https://fiskit.com.ar/developers

Coding with an AI agent? Point it to https://fiskit.com.ar/llms.txt: the API rules, errors and examples in plain text.

---

## Español

Cliente oficial de la API de [FisKit](https://fiskit.com.ar): facturas electrónicas de ARCA (ex AFIP) con un solo pedido. Para Go 1.21 o superior, sin dependencias.

```go
client, err := fiskit.New("") // lee FISKIT_API_KEY

// Con el id de tu venta como clave, un reintento nunca duplica la factura
factura, err := client.Invoices.Create(ctx, map[string]any{
	"voucher_type": "invoice_c",
	"amounts":      map[string]any{"total": 1500},
}, "venta-8812")
```

Los errores de la API son `*fiskit.Error`, con `Code`, `Message`, `Param` y `RequestID`. Para los webhooks, `fiskit.VerifyWebhook` verifica la firma y devuelve el evento.

Las claves `sk_test_...` emiten en homologación, sin validez fiscal; las `sk_live_...` emiten facturas reales.

Documentación completa: https://fiskit.com.ar/developers

¿Programás con un agente de IA? Pasale https://fiskit.com.ar/llms.txt: las reglas, errores y ejemplos de la API en texto plano.
