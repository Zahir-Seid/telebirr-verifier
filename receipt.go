package telebirr

// Receipt holds the fields extracted from a Telebirr payment receipt.
//
// All fields are raw strings exactly as they appear on the receipt page;
// amounts keep their formatting (e.g. "1,500.00 Birr"). Empty strings mean
// the value was not present or could not be extracted.
type Receipt struct {
	PayerName              string // "የከፋይ ስም/Payer Name"
	PayerTelebirrNo        string // "የከፋይ ቴሌብር ቁ./Payer telebirr no."
	CreditedPartyName      string // "የገንዘብ ተቀባይ ስም/Credited Party name"
	CreditedPartyAccountNo string // "የገንዘብ ተቀባይ ቴሌብር ቁ./Credited party account no"
	TransactionStatus      string // "የክፍያው ሁኔታ/transaction status"
	ReceiptNo              string
	PaymentDate            string // format DD-MM-YYYY HH:MM:SS
	SettledAmount          string
	ServiceFee             string
	ServiceFeeVAT          string
	TotalPaidAmount        string
	BankName               string // set only for bank transfers
	CustomerNote           string
}

// IsValid reports whether the receipt carries the minimum fields required to
// trust a verification result: a receipt number, a payer name and a
// transaction status.
func (r Receipt) IsValid() bool {
	return r.ReceiptNo != "" && r.PayerName != "" && r.TransactionStatus != ""
}
