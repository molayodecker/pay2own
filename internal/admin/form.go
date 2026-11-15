package admin

import (
	"math"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"pay2own.com/backend/internal/store"
)

var imeiPattern = regexp.MustCompile(`^[0-9]{15}$`)

type saleForm struct {
	Name     string
	Phone    string
	Provider string
	IMEI     string
	Model    string
	Price    string
	Deposit  string
	Weeks    string
	FirstDue string
}

func (f saleForm) sale() (store.Sale, string) {
	f.Name = strings.TrimSpace(f.Name)
	f.Phone = strings.TrimSpace(f.Phone)
	f.Provider = strings.TrimSpace(f.Provider)
	f.IMEI = strings.TrimSpace(f.IMEI)
	f.Model = strings.TrimSpace(f.Model)
	if f.Name == "" || f.Phone == "" || f.Model == "" || f.FirstDue == "" {
		return store.Sale{}, "Name, phone, model, and first due date are required."
	}
	if f.Provider != "mtn" && f.Provider != "vod" && f.Provider != "atl" {
		return store.Sale{}, "Network must be MTN, Telecel, or AirtelTigo."
	}
	if !imeiPattern.MatchString(f.IMEI) {
		return store.Sale{}, "IMEI must be 15 digits."
	}
	price, err := ghsToMinor(f.Price)
	if err != nil || price <= 0 {
		return store.Sale{}, "Price must be a number of cedis."
	}
	deposit, err := ghsToMinor(f.Deposit)
	if err != nil || deposit < 0 {
		return store.Sale{}, "Deposit must be a number of cedis."
	}
	weeks, err := strconv.Atoi(strings.TrimSpace(f.Weeks))
	if err != nil || weeks < 1 || weeks > 104 {
		return store.Sale{}, "Weeks must be from 1 to 104."
	}
	due, err := time.Parse("2006-01-02", strings.TrimSpace(f.FirstDue))
	if err != nil {
		return store.Sale{}, "First due date must be YYYY-MM-DD."
	}
	return store.Sale{
		FullName: f.Name, Phone: f.Phone, Provider: f.Provider, IMEI: f.IMEI, Model: f.Model,
		PriceMinor: price, DepositMinor: deposit, Weeks: weeks, FirstDue: due, Restrict: true,
	}, ""
}

func ghsToMinor(raw string) (int64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, nil
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0, err
	}
	return int64(math.Round(value * 100)), nil
}

func enrollURL(imei, token string) string {
	q := url.Values{}
	q.Set("token", token)
	q.Set("imei", imei)
	return "pay2own://enroll?" + q.Encode()
}

func cedis(minor int64) string {
	sign := ""
	if minor < 0 {
		sign = "-"
		minor = -minor
	}
	return sign + strconv.FormatInt(minor/100, 10) + "." + strconv.FormatInt(minor%100+100, 10)[1:]
}
