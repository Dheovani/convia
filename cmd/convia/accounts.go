package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"text/tabwriter"
	"time"

	"convia/internal/accounts"
	"convia/internal/applications"
	"convia/internal/config"
	"convia/internal/database"
	"convia/internal/users"
)

/*
accountCommand lets whoever runs Convia stop somebody signing in, and let them
back.

It no longer creates accounts. A person registers their own from the sign-in
page, with a password nobody else ever sees — which is what lets that password
seal the account's key. An operator creating the account would have to know the
password, and the key would be theirs to open.
*/
func accountCommand(ctx context.Context, logger *slog.Logger, cfg config.Config, arguments []string) error {
	if len(arguments) == 0 {
		fmt.Print(usage)
		return errors.New("account requires one of suspend or activate")
	}

	pool, err := database.Open(ctx, cfg.Database, logger)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer pool.Close()

	applicationService := applications.NewService(applications.NewStore(pool), logger)
	userService := users.NewService(users.NewStore(pool), applicationService, logger)
	service := accounts.NewService(accounts.NewStore(pool), userService, applications.FirstPartyID, logger)

	switch arguments[0] {
	case "suspend":
		return changeAccount(ctx, service, arguments[1:], accounts.StatusSuspended)
	case "activate":
		return changeAccount(ctx, service, arguments[1:], accounts.StatusActive)
	default:
		fmt.Print(usage)
		return fmt.Errorf("unknown account command %q", arguments[0])
	}
}

// changeAccount suspends or restores somebody's ability to sign in.
func changeAccount(ctx context.Context, service *accounts.Service, arguments []string, status accounts.Status) error {
	if len(arguments) != 1 {
		return fmt.Errorf("account %s requires exactly one account identifier", verbFor(status))
	}

	var (
		account accounts.Account
		err     error
	)
	if status == accounts.StatusSuspended {
		account, err = service.Suspend(ctx, arguments[0])
	} else {
		account, err = service.Activate(ctx, arguments[0])
	}
	if err != nil {
		return fmt.Errorf("%s account: %w", verbFor(status), err)
	}

	writer := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintf(writer, "ACCOUNT\tUSERNAME\tSTATUS\tUPDATED\n")
	fmt.Fprintf(writer, "%s\t%s\t%s\t%s\n", account.ID, account.Username, account.Status,
		account.UpdatedAt.Format(time.RFC3339))
	if err := writer.Flush(); err != nil {
		return fmt.Errorf("write the account: %w", err)
	}

	if status == accounts.StatusSuspended {
		fmt.Print("\nExisting sessions stop working on their next request.\n")
	}
	return nil
}

func verbFor(status accounts.Status) string {
	if status == accounts.StatusSuspended {
		return "suspend"
	}
	return "activate"
}
