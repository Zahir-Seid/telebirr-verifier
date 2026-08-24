package telebirr

import (
	"os"
	"testing"
)

func loadFixture(t *testing.T, name string) string {
	t.Helper()
	body, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return string(body)
}

func TestScrapeReceipt(t *testing.T) {
	receipt := ScrapeReceipt(loadFixture(t, "receipt.html"))

	expected := Receipt{
		PayerName:              "ABEBE KEBEDE",
		PayerTelebirrNo:        "0711234567",
		CreditedPartyName:      "SOLOMON TESFAYE",
		CreditedPartyAccountNo: "0729876543",
		TransactionStatus:      "Successful",
		ReceiptNo:              "CBJ0H74269",
		PaymentDate:            "24-08-2026 14:35:12",
		SettledAmount:          "1,500.00 Birr",
		ServiceFee:             "15.00 Birr",
		ServiceFeeVAT:          "1.95 Birr",
		TotalPaidAmount:        "1,516.95 Birr",
		BankName:               "",
		CustomerNote:           "August rent",
	}
	if receipt != expected {
		t.Errorf("ScrapeReceipt() =\n%+v\nwant\n%+v", receipt, expected)
	}
	if !receipt.IsValid() {
		t.Error("ScrapeReceipt() produced an invalid receipt for a complete fixture")
	}
}

func TestScrapeReceipt_BankTransfer(t *testing.T) {
	receipt := ScrapeReceipt(loadFixture(t, "receipt-bank.html"))

	switch {
	case receipt.BankName != "Commercial Bank of Ethiopia":
		t.Errorf("BankName = %q, want %q", receipt.BankName, "Commercial Bank of Ethiopia")
	case receipt.CreditedPartyAccountNo != "1000123456789":
		t.Errorf("CreditedPartyAccountNo = %q, want %q", receipt.CreditedPartyAccountNo, "1000123456789")
	case receipt.CreditedPartyName != "Bole Branch Abebe Kebede":
		t.Errorf("CreditedPartyName = %q, want %q", receipt.CreditedPartyName, "Bole Branch Abebe Kebede")
	case receipt.ReceiptNo != "FT2519ABCD":
		t.Errorf("ReceiptNo = %q, want %q", receipt.ReceiptNo, "FT2519ABCD")
	case receipt.SettledAmount != "8,750.00 Birr":
		t.Errorf("SettledAmount = %q, want %q", receipt.SettledAmount, "8,750.00 Birr")
	case !receipt.IsValid():
		t.Error("IsValid() = false for a complete bank-transfer fixture")
	}
}

func TestScrapeReceipt_Malformed(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{"empty body", ""},
		{"plain text", "not html at all"},
		{"error page", "<html><body><h1>Service Unavailable</h1></body></html>"},
		{"truncated table", `<table><tr><td class="receipttableTd">የከፋይ ስም/Payer Name</td>`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			receipt := ScrapeReceipt(tt.body)
			if (receipt != Receipt{}) {
				t.Errorf("ScrapeReceipt(%q) = %+v, want zero receipt", tt.body, receipt)
			}
			if receipt.IsValid() {
				t.Error("IsValid() = true, want false")
			}
		})
	}
}

func TestScrapeReceipt_ReceiptNoFallback(t *testing.T) {
	// The primary extractor only accepts alphanumeric-only cells; here the
	// first candidate cells are disqualified and the second combined-class
	// cell must win.
	body := `<table>
		<tr><td class="receipttableTd receipttableTd2">not-a-receipt</td></tr>
		<tr><td class="receipttableTd receipttableTd2">CBJ0H74-269</td></tr>
	</table>`
	receipt := ScrapeReceipt(body)
	if receipt.ReceiptNo != "CBJ0H74-269" {
		t.Errorf("ReceiptNo = %q, want %q", receipt.ReceiptNo, "CBJ0H74-269")
	}
}

func TestExtractSettledAmount_Patterns(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		expected string
	}{
		{
			name:     "direct label structure",
			body:     `<td>የተከፈለው መጠን/Settled Amount</td><td class="x">250.75 Birr</td>`,
			expected: "250.75 Birr",
		},
		{
			name:     "table row structure",
			body:     `<tr><td>የተከፈለው መጠን/Settled Amount</td><td>1,000 Birr</td></tr>`,
			expected: "1,000 Birr",
		},
		{
			name:     "loose mention",
			body:     `<p>Your Settled Amount today is 42 Birr.</p>`,
			expected: "42 Birr",
		},
		{
			name:     "absent",
			body:     `<p>nothing here</p>`,
			expected: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractSettledAmount(tt.body)
			if got != tt.expected {
				t.Errorf("extractSettledAmount() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestExtractServiceFee_SkipsVAT(t *testing.T) {
	body := `<tr>
		<td>የአገልግሎት ክፍያ/Service fee VAT</td><td>1.95 Birr</td>
	</tr>
	<tr>
		<td>የአገልግሎት ክፍያ/Service fee</td><td>15.00 Birr</td>
	</tr>`
	if got := extractServiceFee(body); got != "15.00 Birr" {
		t.Errorf("extractServiceFee() = %q, want %q (VAT row must be skipped)", got, "15.00 Birr")
	}

	vatOnlyBody := `<tr><td>የአገልግሎት ክፍያ/Service fee VAT</td><td>1.95 Birr</td></tr>`
	if got := extractServiceFee(vatOnlyBody); got != "" {
		t.Errorf("extractServiceFee(vat only) = %q, want empty", got)
	}
}

func TestReceipt_IsValid(t *testing.T) {
	tests := []struct {
		name    string
		receipt Receipt
		isValid bool
	}{
		{name: "complete receipt", receipt: Receipt{ReceiptNo: "X1", PayerName: "A B", TransactionStatus: "Successful"}, isValid: true},
		{name: "missing receipt no", receipt: Receipt{PayerName: "A B", TransactionStatus: "Successful"}, isValid: false},
		{name: "missing payer name", receipt: Receipt{ReceiptNo: "X1", TransactionStatus: "Successful"}, isValid: false},
		{name: "missing status", receipt: Receipt{ReceiptNo: "X1", PayerName: "A B"}, isValid: false},
		{name: "zero value", receipt: Receipt{}, isValid: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.receipt.IsValid(); got != tt.isValid {
				t.Errorf("IsValid() = %v, want %v", got, tt.isValid)
			}
		})
	}
}
