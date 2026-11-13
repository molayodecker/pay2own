package main

import (
	"context"
	"fmt"
	"os"

	"pay2own.com/backend/internal/store"
)

const usage = "usage: pay2own-admin issue-token <15-digit IMEI>"

func main() {
	if len(os.Args) != 3 || os.Args[1] != "issue-token" {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}

	if err := issueToken(os.Args[2]); err != nil {
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