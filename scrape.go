package telebirr

import (
	"html"
	"regexp"
	"strings"
	"unicode"
)

// Receipt page labels. Each appears in a label cell followed by a value cell.
const (
	labelPayerName              = "የከፋይ ስም/Payer Name"
	labelPayerTelebirrNo        = "የከፋይ ቴሌብር ቁ./Payer telebirr no."
	labelCreditedPartyName      = "የገንዘብ ተቀባይ ስም/Credited Party name"
	labelCreditedPartyAccountNo = "የገንዘብ ተቀባይ ቴሌብር ቁ./Credited party account no"
	labelTransactionStatus      = "የክፍያው ሁኔታ/transaction status"
	labelBankAccountNumber      = "የባንክ አካውንት ቁጥር/Bank account number"
	labelServiceFeeVAT          = "የአገልግሎት ክፍያ ተ.እ.ታ/Service fee VAT"
	labelTotalPaidAmount        = "ጠቅላላ የተከፈለ/Total Paid Amount"
	labelCustomerNote           = "የደንበኛ መልዕክት/Customer Note"

	amharicServiceFee = "የአገልግሎት ክፍያ"
	amharicSettled    = "የተከፈለው መጠን"
	amharicVATMarker  = "ተ.እ.ታ"
)

const birrAmount = `([\d,]+(?:\.\d+)?\s+Birr)`

// All patterns are compiled once at package level.
var (
	settledAmountPatterns = []*regexp.Regexp{
		// Direct match with the exact label structure.
		regexp.MustCompile(`(?is)የተከፈለው\s+መጠን/Settled\s+Amount.*?</td>\s*<td[^>]*>\s*` + birrAmount),
		// Table row containing the label.
		regexp.MustCompile(`(?is)<tr[^>]*>.*?የተከፈለው\s+መጠን/Settled\s+Amount.*?<td[^>]*>\s*` + birrAmount),
		// Any cell mentioning "Settled Amount" followed by an amount.
		regexp.MustCompile(`(?is)Settled\s+Amount.*?` + birrAmount),
		// Third cell of the first data row inside the transaction details table.
		regexp.MustCompile(`(?is)የክፍያ\s+ዝርዝር/Transaction\s+details.*?<tr[^>]*>.*?<td[^>]*>\s*[^<]*</td>\s*<td[^>]*>\s*[^<]*</td>\s*<td[^>]*>\s*` + birrAmount),
	}

	// The TypeScript original uses a negative lookahead to exclude the VAT
	// row; Go's RE2 engine has no lookahead, so group 1 captures whatever
	// trails the label and the caller rejects matches carrying a VAT marker.
	serviceFeePattern = regexp.MustCompile(
		`(?i)የአገልግሎት\s+ክፍያ/Service\s+fee([^\n<]*)</td>\s*<td[^>]*>\s*` + birrAmount)

	receiptNoPattern = regexp.MustCompile(
		`(?i)<td[^>]*class="[^"]*receipttableTd[^"]*receipttableTd2[^"]*"[^>]*>\s*([A-Z0-9]+)\s*</td>`)

	receiptDatePattern = regexp.MustCompile(`(\d{2}-\d{2}-\d{4}\s+\d{2}:\d{2}:\d{2})`)

	bankAccountPattern = regexp.MustCompile(`(\d+)\s+(.*)`)

	tagPattern = regexp.MustCompile(`<[^>]*>`)

	tdValueCellPattern = regexp.MustCompile(
		`(?i)<td[^>]*class="[^"]*receipttableTd[^"]*receipttableTd2[^"]*"[^>]*>\s*([^<]*)\s*</td>`)

	dateCellPattern = regexp.MustCompile(
		`(?i)<td[^>]*class="[^"]*receipttableTd[^"]*"[^>]*>\s*([^<]*-202[^<]*)\s*</td>`)

	rowPattern = regexp.MustCompile(`(?is)<tr[^>]*>(.*?)</tr>`)

	cellPattern = regexp.MustCompile(`<td[^>]*>\s*([^<]*)\s*</td>`)

	fieldLabels = []string{
		labelPayerName,
		labelPayerTelebirrNo,
		labelCreditedPartyName,
		labelCreditedPartyAccountNo,
		labelTransactionStatus,
		labelBankAccountNumber,
		labelServiceFeeVAT,
		labelTotalPaidAmount,
		labelCustomerNote,
	}

	fieldPatternCache = newFieldPatterns()
)

func newFieldPatterns() map[string]*regexp.Regexp {
	patterns := make(map[string]*regexp.Regexp, len(fieldLabels))
	for _, label := range fieldLabels {
		pattern := `(?i)` + regexp.QuoteMeta(label) + `.*?</td>\s*<td[^>]*>\s*([^<]+)`
		patterns[label] = regexp.MustCompile(pattern)
	}
	return patterns
}

// ScrapeReceipt extracts receipt fields from the raw HTML of a Telebirr
// transaction page. Fields that cannot be located are left empty; callers can
// validate the result with [Receipt.IsValid].
func ScrapeReceipt(body string) Receipt {
	var r Receipt

	r.PayerName = extractField(body, labelPayerName)
	r.PayerTelebirrNo = extractField(body, labelPayerTelebirrNo)
	r.TransactionStatus = extractField(body, labelTransactionStatus)
	r.ReceiptNo = extractReceiptNo(body)
	r.PaymentDate = extractPaymentDate(body)
	r.SettledAmount = extractSettledAmount(body)
	r.ServiceFee = extractServiceFee(body)
	r.ServiceFeeVAT = extractField(body, labelServiceFeeVAT)
	r.TotalPaidAmount = extractField(body, labelTotalPaidAmount)

	creditedPartyName := extractField(body, labelCreditedPartyName)
	creditedPartyAccountNo := extractField(body, labelCreditedPartyAccountNo)

	// Bank transfers list a bank account number instead of a Telebirr
	// account: "<number> <bank branch/name>". The original credited party
	// name is then the bank itself, while the real beneficiary name and
	// account number come out of the account cell.
	if bankAccountRaw := extractField(body, labelBankAccountNumber); bankAccountRaw != "" {
		r.BankName = creditedPartyName
		if m := bankAccountPattern.FindStringSubmatch(bankAccountRaw); m != nil {
			creditedPartyAccountNo = strings.TrimSpace(m[1])
			creditedPartyName = strings.TrimSpace(m[2])
		}
	}
	r.CreditedPartyName = creditedPartyName
	r.CreditedPartyAccountNo = creditedPartyAccountNo

	r.CustomerNote = extractField(body, labelCustomerNote)
	return r
}

func extractField(body, label string) string {
	// Skip candidates whose value cell holds only decoration (e.g. a bare
	// ":" separator column) rather than real content.
	for _, match := range fieldPatternCache[label].FindAllStringSubmatch(body, -1) {
		value := cleanValue(match)
		if hasContent(value) {
			return value
		}
	}
	return ""
}

func hasContent(value string) bool {
	for _, r := range value {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return true
		}
	}
	return false
}

func extractReceiptNo(body string) string {
	if m := receiptNoPattern.FindStringSubmatch(body); m != nil {
		return strings.TrimSpace(m[1])
	}
	// Fallback: second receipttableTd2 cell holds the value, not the label.
	cells := tdValueCellPattern.FindAllStringSubmatch(body, -1)
	if len(cells) > 1 && cells[1][1] != "" {
		return strings.TrimSpace(cells[1][1])
	}
	return ""
}

func extractPaymentDate(body string) string {
	if m := receiptDatePattern.FindStringSubmatch(body); m != nil {
		return strings.TrimSpace(m[1])
	}
	// Fallback: first receipt table cell containing "-202" (e.g. "24-08-2026 ...").
	if cells := dateCellPattern.FindAllStringSubmatch(body, 1); len(cells) == 1 {
		return cleanValue([]string{"", cells[0][1]})
	}
	return ""
}

func extractSettledAmount(body string) string {
	for _, pattern := range settledAmountPatterns {
		if m := pattern.FindStringSubmatch(body); m != nil {
			return strings.TrimSpace(m[1])
		}
	}
	return lastCellOfLabeledRow(body, amharicSettled, "Settled Amount")
}

func extractServiceFee(body string) string {
	for _, m := range serviceFeePattern.FindAllStringSubmatch(body, -1) {
		trailing := strings.ToLower(m[1])
		// Skip the VAT variant of the label.
		if strings.Contains(trailing, amharicVATMarker) || strings.Contains(trailing, "vat") {
			continue
		}
		return strings.TrimSpace(m[2])
	}
	return lastCellOfLabeledRow(body, amharicServiceFee, "Service fee")
}

// lastCellOfLabeledRow mirrors the cheerio row-scan fallbacks: find the table
// row whose text mentions the label (without VAT markers for fees) and return
// its last cell content.
func lastCellOfLabeledRow(body, amharicLabel, englishLabel string) string {
	for _, row := range rowPattern.FindAllStringSubmatch(body, -1) {
		rowText := stripTags(row[1])
		isServiceRow := amharicLabel == amharicServiceFee
		matched := strings.Contains(rowText, amharicLabel) || strings.Contains(strings.ToLower(rowText), strings.ToLower(englishLabel))
		if !matched || (isServiceRow && (strings.Contains(rowText, amharicVATMarker) || strings.Contains(strings.ToLower(rowText), "vat"))) {
			continue
		}
		cells := cellPattern.FindAllStringSubmatch(row[1], -1)
		for i := len(cells) - 1; i >= 0; i-- {
			if v := strings.TrimSpace(cells[i][1]); v != "" {
				return cleanValue([]string{"", v})
			}
		}
	}
	return ""
}

// cleanValue strips residual tags, decodes HTML entities and trims space.
func cleanValue(match []string) string {
	if len(match) < 2 {
		return ""
	}
	return strings.TrimSpace(stripTags(match[1]))
}

func stripTags(s string) string {
	return html.UnescapeString(tagPattern.ReplaceAllString(s, ""))
}
