package payment

import "testing"

func TestValidateIntent(t *testing.T) {
	valid := Intent{ResourceType: "activity_registration", ResourceID: "1", AmountCents: 1, Currency: "CNY", Method: "wechat", IdempotencyKey: "key"}
	if err := ValidateIntent(valid); err != nil {
		t.Fatalf("ValidateIntent() error = %v", err)
	}
	valid.Currency = "USD"
	if err := ValidateIntent(valid); err == nil {
		t.Fatal("ValidateIntent() accepted unsupported currency")
	}
}
