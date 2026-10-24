package cmd

import (
	"errors"
	"fmt"

	"github.com/CupThread/CupThreadAgenticCoding/internal/api"
	"github.com/CupThread/CupThreadAgenticCoding/internal/auth"
	"github.com/spf13/cobra"
)

func newBillingCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "billing",
		Short: "Show usage and manage the subscription (Polar)",
		Long: `Show usage and manage the subscription (Polar).

show and intake-ledger work for every workspace role and with cpt_ API
tokens. checkout, portal, and addons require an interactive Clerk
session, and no CLI credential qualifies — personal access tokens and
OAuth logins are both cpt_ tokens, so they fail with 403
interactive_session_required; perform these actions in the Console web
UI (Console → Billing). The intake-ledger reset endpoint needs the
interactive privacy.manage capability, so it is likewise Console-only
and has no CLI counterpart.`,
	}
	cmd.AddCommand(newBillingShowCmd(), newBillingIntakeLedgerCmd(), newBillingCheckoutCmd(), newBillingPortalCmd(), newBillingAddonsCmd())
	return cmd
}

func newBillingShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:                   "show",
		Short:                 "Show subscription tier, limits, and current usage",
		DisableFlagsInUseLine: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := workspaceClient(cmd.Context())
			if err != nil {
				return err
			}
			var usage api.SubscriptionUsage
			if err := A.client.Do(cmd.Context(), "GET", wsPath(ws, "/billing"), nil, nil, &usage); err != nil {
				return err
			}
			if A.structured() {
				return A.out.Structured(usage)
			}
			A.out.Table([]string{"Field", "Value"}, [][]string{
				{"Tier", usage.Tier + " (" + usage.Status + ")"},
				{"Monthly price", fmt.Sprintf("$%.2f", usage.MonthlyPrice)},
				{"Apps", fmt.Sprintf("%d / %d", usage.Usage.Apps, usage.Limits.Apps+usage.ExtraApps)},
				{"Members", fmt.Sprintf("%d / %d", usage.Usage.Members, usage.Limits.Members+usage.ExtraMembers)},
				{"Submissions this month", fmt.Sprintf("%d / %d", usage.Usage.SubmissionsThisMonth, usage.Limits.SubmissionsPerMonth)},
				{"Extra apps", fmt.Sprintf("%d", usage.ExtraApps)},
				{"Extra members", fmt.Sprintf("%d", usage.ExtraMembers)},
			})
			return nil
		},
	}
}

func newBillingIntakeLedgerCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "intake-ledger",
		Short: "Show the monthly intake ledger status (durable accepted count and reset budget)",
		Long: `Show the monthly intake ledger status.

Reports the current UTC month's durable ledger behind the
"Submissions this month" quota in billing show: the accepted count of
feedback submissions plus user-created feature requests (deleting rows
never gives the month's intake back), the tier cap it is enforced
against, and the audited per-month operator reset budget.`,
		DisableFlagsInUseLine: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := workspaceClient(cmd.Context())
			if err != nil {
				return err
			}
			var status api.IntakeLedgerStatus
			if err := A.client.Do(cmd.Context(), "GET", wsPath(ws, "/intake-ledger"), nil, nil, &status); err != nil {
				return err
			}
			if A.structured() {
				return A.out.Structured(status)
			}
			A.out.Table([]string{"Field", "Value"}, [][]string{
				{"UTC month", status.UTCMonth},
				{"Accepted intake", fmt.Sprintf("%d / %d", status.AcceptedCount, status.Cap)},
				{"Resets used this month", fmt.Sprintf("%d", status.ResetsUsed)},
				{"Resets remaining this month", fmt.Sprintf("%d", status.ResetsRemaining)},
			})
			return nil
		},
	}
}

func newBillingCheckoutCmd() *cobra.Command {
	var extraApps, extraMembers int
	var open bool
	checkout := &cobra.Command{
		Use:                   "checkout",
		Short:                 "Create a Pro upgrade checkout and print its URL",
		DisableFlagsInUseLine: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := workspaceClient(cmd.Context())
			if err != nil {
				return err
			}
			body := map[string]any{"tier": "pro", "extraApps": extraApps, "extraMembers": extraMembers}
			var resp api.CheckoutSession
			if err := A.client.Do(cmd.Context(), "POST", wsPath(ws, "/billing/checkout"), nil, body, &resp); err != nil {
				return err
			}
			if A.structured() {
				return A.out.Structured(resp)
			}
			if resp.CheckoutURL == nil {
				msg := "billing is not configured on the server"
				if resp.Message != nil {
					// A 2xx body field returned as the command error, so
					// cobra prints it to stderr raw — it must pass through
					// the strict stripper (plus the serverErrorText length
					// cap) first, exactly like the APIError decode path
					// (issue #298).
					msg = api.ServerMessageText(*resp.Message)
				}
				return errors.New(msg)
			}
			A.out.PrintfSafe("Checkout URL: %s", *resp.CheckoutURL)
			if open {
				if err := auth.OpenBrowser(*resp.CheckoutURL); err != nil {
					A.out.Printf("(could not open a browser; use the URL above)")
				}
			}
			return nil
		},
	}
	checkout.Flags().IntVar(&extraApps, "extra-apps", 0, "Extra app add-ons to include (0-500)")
	checkout.Flags().IntVar(&extraMembers, "extra-members", 0, "Extra member add-ons to include (0-500)")
	checkout.Flags().BoolVar(&open, "open", false, "Open the checkout URL in a browser")
	return checkout
}

func newBillingPortalCmd() *cobra.Command {
	var open bool
	portal := &cobra.Command{
		Use:                   "portal",
		Short:                 "Print the Polar billing portal URL",
		DisableFlagsInUseLine: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := workspaceClient(cmd.Context())
			if err != nil {
				return err
			}
			var resp api.BillingPortalResponse
			if err := A.client.Do(cmd.Context(), "GET", wsPath(ws, "/billing/portal"), nil, nil, &resp); err != nil {
				return err
			}
			if A.structured() {
				return A.out.Structured(resp)
			}
			if resp.PortalURL == nil {
				msg := "billing is not configured on the server"
				if resp.Message != nil {
					// Same 2xx-field strict stripper as the checkout site
					// above (issue #298): cobra prints the message raw on
					// stderr, so the terminal controls cannot survive.
					msg = api.ServerMessageText(*resp.Message)
				}
				return errors.New(msg)
			}
			A.out.PrintfSafe("Portal URL: %s", *resp.PortalURL)
			if open {
				if err := auth.OpenBrowser(*resp.PortalURL); err != nil {
					A.out.Printf("(could not open a browser; use the URL above)")
				}
			}
			return nil
		},
	}
	portal.Flags().BoolVar(&open, "open", false, "Open the portal URL in a browser")
	return portal
}

func newBillingAddonsCmd() *cobra.Command {
	var extraApps, extraMembers int
	addons := &cobra.Command{
		Use:                   "addons",
		Short:                 "Update the workspace's extra app/member add-ons",
		DisableFlagsInUseLine: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := workspaceClient(cmd.Context())
			if err != nil {
				return err
			}
			body := map[string]int{"extraApps": extraApps, "extraMembers": extraMembers}
			var usage api.SubscriptionUsage
			if err := A.client.Do(cmd.Context(), "PUT", wsPath(ws, "/billing/addons"), nil, body, &usage); err != nil {
				return err
			}
			if A.structured() {
				return A.out.Structured(usage)
			}
			A.out.Printf("✓ Add-ons updated: %d extra apps, %d extra members (monthly $%.2f)",
				usage.ExtraApps, usage.ExtraMembers, usage.MonthlyPrice)
			return nil
		},
	}
	addons.Flags().IntVar(&extraApps, "extra-apps", 0, "Extra app add-ons (0-500)")
	addons.Flags().IntVar(&extraMembers, "extra-members", 0, "Extra member add-ons (0-500)")
	return addons
}
