package main

type Installment struct {
	ID          string `json:"id"`
	Currency    string `json:"currency"`
	AmountMinor int64  `json:"amount_minor"`
	Status      string `json:"status"`
	Demo        bool   `json:"demo"`
}

func demoInstallment() Installment {
	return Installment{
		ID:          "demo-001",
		Currency:    "GHS",
		AmountMinor: 10000,
		Status:      "unpaid",
		Demo:        true,
	}
}
