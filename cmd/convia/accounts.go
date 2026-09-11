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
accountCommand administers the people who can sign in to Convia's own product.

It exists because there is no self-service sign-up, and there is no
self-service sign-up because open registration needs email verification, which
needs a mailer Convia does not have. Somebody with database access creates the
account and hands over the password Convia generated.

That is the same bootstrap `convia operator issue` answers, for the same
reason and with the same property: the digest, the identifier format, and the
validation are the code the service uses rather than a second implementation
somebody has to keep in step.
*/
func accountCommand(ctx context.Context, logger *slog.Logger, cfg config.Config, arguments []string) error {
	if len(arguments) == 0 {
		fmt.Print(usage)
		return errors.New("account requires one of create, list, suspend, or activate")
	}

	if cfg.FirstPartyApplication == "" {
		return fmt.Errorf("set %s before administering accounts: an account belongs to the "+
			"application that owns Convia's own product", "CONVIA_FIRST_PARTY_APPLICATION")
	}

	pool, err := database.Open(ctx, cfg.Database, logger)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer pool.Close()

	applicationService := applications.NewService(applications.NewStore(pool), logger)
	userService := users.NewService(users.NewStore(pool), applicationService, logger)
	service := accounts.NewService(accounts.NewStore(pool), userService, cfg.FirstPartyApplication, logger)

	switch arguments[0] {
	case "create":
		return createAccount(ctx, service, arguments[1:])
	case "suspend":
		return changeAccount(ctx, service, arguments[1:], accounts.StatusSuspended)
	case "activate":
		return changeAccount(ctx, service, arguments[1:], accounts.StatusActive)
	default:
		fmt.Print(usage)
		return fmt.Errorf("unknown account command %q", arguments[0])
	}
}

/*
createAccount makes an account and prints its password once.

The password goes to standard output rather than the structured log, for the
reason the operator command already gives: the log is shipped, retained, and
read by people who should not receive credentials, while standard output is
what the person running the command is already looking at.

It is **generated rather than chosen**, and the operator is never offered the
choice. An operator picking passwords reuses one across the accounts they
create, and a generated twenty-six-character secret makes online guessing a
non-question rather than a limit to tune.
*/
func createAccount(ctx context.Context, service *accounts.Service, arguments []string) error {
	if len(arguments) < 2 {
		return errors.New(`account create requires an email and a display name, ` +
			`for example: convia account create ana@example.com "Ana Ribeiro"`)
	}

	account, password, err := service.Create(ctx, accounts.Registration{
		Email:       arguments[0],
		DisplayName: arguments[1],
	})
	if err != nil {
		return fmt.Errorf("create account: %w", err)
	}

	fmt.Printf("Created account %s for %s (%s)\n", account.ID, account.DisplayName, account.Email)
	fmt.Printf("Convia user: %s\n\n", account.UserID)
	fmt.Printf("%s\n\n", string(password))
	fmt.Print("This password is shown once and is not stored. Convia cannot show it again.\n")
	fmt.Print("Hand it over out of band, and ask them to change it after signing in.\n")
	return nil
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
	fmt.Fprintf(writer, "ACCOUNT\tEMAIL\tSTATUS\tUPDATED\n")
	fmt.Fprintf(writer, "%s\t%s\t%s\t%s\n", account.ID, account.Email, account.Status,
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
