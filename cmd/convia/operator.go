package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"text/tabwriter"
	"time"

	"convia/internal/config"
	"convia/internal/database"
	"convia/internal/operator"
)

/*
operatorCommand administers operator credentials from the command line.

This exists to answer a bootstrap that has no other answer: issuing an operator
credential over the API requires presenting one, so the first must come from
somewhere else. That somewhere is direct database access, which is the right
authority to mint the key that administers Convia — whoever has it could write
the row by hand regardless, and this way the digest, the identifier format, and
the scope validation are the same code the service uses.

It doubles as the tooling the revocation runbook needs. Withdrawing a key
during an incident should not require composing SQL under pressure.
*/
func operatorCommand(ctx context.Context, logger *slog.Logger, cfg config.Config, arguments []string) error {
	if len(arguments) == 0 {
		fmt.Print(usage)
		return errors.New("operator requires one of issue, list, or revoke")
	}

	pool, err := database.Open(ctx, cfg.Database, logger)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer pool.Close()

	service := operator.NewService(operator.NewStore(pool), logger)

	switch arguments[0] {
	case "issue":
		return issueOperator(ctx, service, arguments[1:])
	case "list":
		return listOperators(ctx, service)
	case "revoke":
		return revokeOperator(ctx, service, arguments[1:])
	default:
		fmt.Print(usage)
		return fmt.Errorf("unknown operator command %q", arguments[0])
	}
}

/*
issueOperator mints a credential and prints its secret once.

The secret goes to standard output rather than the structured log on purpose:
the log is shipped, retained, and read by people who should not receive key
material, while standard output is what the person running the command is
already looking at.
*/
func issueOperator(ctx context.Context, service *operator.Service, arguments []string) error {
	if len(arguments) == 0 {
		return errors.New("operator issue requires a name")
	}

	scopes := operator.Scopes()
	if len(arguments) > 1 {
		scopes = make([]operator.Scope, 0, len(arguments)-1)
		for _, value := range arguments[1:] {
			scopes = append(scopes, operator.Scope(value))
		}
	}

	credential, secret, err := service.Issue(ctx, operator.Request{Name: arguments[0], Scopes: scopes})
	if err != nil {
		return fmt.Errorf("issue operator credential: %w", err)
	}

	fmt.Printf("Issued operator credential %s (%s)\n", credential.ID, credential.Name)
	fmt.Printf("Scopes: %v\n\n", credential.Scopes)
	fmt.Printf("%s\n\n", operator.Token(credential.ID, secret))
	fmt.Print("This secret is shown once and is not stored. Convia cannot show it again.\n")
	return nil
}

// listOperators reports every operator credential and its current state.
func listOperators(ctx context.Context, service *operator.Service) error {
	page, err := service.List(ctx, operator.ListOptions{Limit: 100})
	if err != nil {
		return fmt.Errorf("list operator credentials: %w", err)
	}

	if len(page.Credentials) == 0 {
		fmt.Print("No operator credentials exist. Nobody can administer this instance.\n")
		return nil
	}

	writer := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(writer, "IDENTIFIER\tNAME\tSTATUS\tCREATED\tSCOPES")

	at := time.Now().UTC()
	for _, credential := range page.Credentials {
		fmt.Fprintf(writer, "%s\t%s\t%s\t%s\t%v\n",
			credential.ID,
			credential.Name,
			credential.Status(at),
			credential.CreatedAt.Format(time.RFC3339),
			credential.Scopes,
		)
	}

	if page.NextCursor != "" {
		fmt.Fprintln(writer, "\n(more credentials exist than are shown)")
	}
	return writer.Flush()
}

// revokeOperator withdraws a credential, taking effect on its next request.
func revokeOperator(ctx context.Context, service *operator.Service, arguments []string) error {
	if len(arguments) != 1 {
		return errors.New("operator revoke requires exactly one credential identifier")
	}

	if err := service.Revoke(ctx, arguments[0]); err != nil {
		return fmt.Errorf("revoke operator credential: %w", err)
	}

	fmt.Printf("Revoked %s. It stops authenticating on its next request.\n", arguments[0])
	return nil
}
