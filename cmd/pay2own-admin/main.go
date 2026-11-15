package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"math"
	"os"
	"time"

	"pay2own.com/backend/internal/store"
)

const usage = `usage:
  pay2own-admin issue-token <15-digit IMEI>
  pay2own-admin new-sale -name NAME -phone PHONE -imei IMEI -model MODEL -price GHS -deposit GHS -weeks N -first-due YYYY-MM-DD`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}

	var err error
	switch os.Args[1] {
	case "issue-token":
		if len(os.Args) != 3 {
			fmt.Fprintln(os.Stderr, usage)
			os.Exit(2)
		}
		err = issueToken(os.Args[2])
	case "new-sale":
		err = newSaleCommand(os.Args[2:])
	default:
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func issueToken(imei string) error {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		return fmt.Errorf("DATABASE_URL is required")
	}

	ctx := context.Background()
	db, err := store.Open(ctx, dsn)
	if err != nil {
		return err
	}
	defer db.Close()

	token, err := store.New(db).IssueDeviceToken(ctx, imei)
	if err != nil {
		return err
	}
	fmt.Println(token)
	return nil
}

func newSaleCommand(args []string) error {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		return fmt.Errorf("DATABASE_URL is required")
	}
	ctx := context.Background()
	db, err := store.Open(ctx, dsn)
	if err != nil {
		return err
	}
	defer db.Close()
	return newSale(ctx, store.New(db), args)
}

func newSale(ctx context.Context, st *store.Store, args []string) error {
	fs := flag.NewFlagSet("new-sale", flag.ContinueOnError)
	name := fs.String("name", "", "customer full name")
	phone := fs.String("phone", "", "MoMo number, e.g. 0551234987")
	provider := fs.String("network", "mtn", "mtn, vod or atl")
	imei := fs.String("imei", "", "15-digit IMEI")
	model := fs.String("model", "", "phone model")
	price := fs.Float64("price", 0, "price in GHS")
	deposit := fs.Float64("deposit", 0, "deposit in GHS")
	weeks := fs.Int("weeks", 0, "number of weekly installments")
	first := fs.String("first-due", "", "first due date, YYYY-MM-DD")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *name == "" || *phone == "" || *imei == "" || *model == "" || *first == "" {
		return errors.New("name, phone, imei, model and first-due are required")
	}
	firstDue, err := time.Parse("2006-01-02", *first)
	if err != nil {
		return fmt.Errorf("first-due: %w", err)
	}
	loanID, err := st.CreateSale(ctx, store.Sale{
		FullName: *name, Phone: *phone, Provider: *provider, IMEI: *imei, Model: *model,
		PriceMinor: int64(math.Round(*price * 100)), DepositMinor: int64(math.Round(*deposit * 100)),
		Weeks: *weeks, FirstDue: firstDue,
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "created loan %d\n", loanID)
	return nil
}
